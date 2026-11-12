//go:build darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"offline-complete/internal/agent"
	"offline-complete/internal/config"
	"offline-complete/internal/diag"
	"offline-complete/internal/editstate"
	"offline-complete/internal/generator"
	"offline-complete/internal/history"
	"offline-complete/internal/ipc"
	"offline-complete/internal/rules"
	"offline-complete/internal/session"
	"offline-complete/internal/settings"
	"offline-complete/internal/shellparse"
	"offline-complete/internal/store"
)

const rulesLimit = 16 << 20

// runAgent enables completion mode. It starts the requested shell with edit
// hooks, serves candidates over a private Unix socket and writes selections
// through the same PTY as terminal input.
func runAgent(rulesPath string, args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	if len(args) == 0 {
		return 1, errors.New("usage: OFFLINE_COMPLETE_RULES=pack.json offline-complete SHELL [ARG ...]")
	}
	hooks, runtime, err := newAgentHooks(rulesPath, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "offline-complete: completion disabled: %v\n", err)
		return run(args, stdin, stdout)
	}
	wrap := func(writer io.WriteCloser) io.WriteCloser {
		runtime.base = &lockedWriter{w: writer}
		return &filterWriter{upstream: runtime.base, navigator: runtime.navigator}
	}
	return runWithInputWriter(args, stdin, stdout, wrap, hooks)
}

type agentRuntime struct {
	listener     *ipc.PrivateListener
	sessionID    string
	state        *editstate.State
	pipeR        *os.File
	pipeW        *os.File
	tempDir      string
	stop         chan struct{}
	stopOnce     sync.Once
	accept       chan struct{}
	mu           sync.Mutex
	service      *agent.Service
	base         *lockedWriter
	navigator    *navigator
	window       *exec.Cmd
	generators   *generator.Runner
	logger       *diag.Logger
	startedAt    time.Time
	presencePath string
	disabled     bool
}

func newAgentHooks(rulesPath string, args []string) (hooks runHooks, runtime *agentRuntime, err error) {
	logger, logErr := diag.Open()
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "offline-complete: logging disabled: %v\n", logErr)
	}
	defer func() {
		if err != nil && logger != nil {
			_ = logger.Close()
		}
	}()
	data, err := rules.LoadFile(rulesPath)
	if err != nil {
		return runHooks{}, nil, fmt.Errorf("read rules: %w", err)
	}
	if len(data) > rulesLimit {
		return runHooks{}, nil, errors.New("rule pack too large")
	}
	pack, err := rules.FromJSON(data)
	if err != nil {
		logger.Printf("event=agent_start_error reason=rules err=%q", err)
		return runHooks{}, nil, fmt.Errorf("parse rules: %w", err)
	}
	sum := sha256.Sum256(data)
	logger.Printf("event=agent_start pid=%d rules=%q commands=%d sha256=%s", os.Getpid(), rulesPath, len(pack.Commands), hex.EncodeToString(sum[:]))
	nodeRunner := generator.NewNodeRunner()
	cfg, settingsErr := settings.Load()
	if settingsErr != nil {
		logger.Printf("event=settings_error err=%q", settingsErr)
	}
	nodeRunner.Env = settings.AIEnvironment(cfg.AI)
	if nodePath := config.NodePath(); nodePath != "" {
		logger.Printf("event=generator_node path=%q", nodePath)
	}
	if !nodeRunner.Available() {
		logger.Printf("event=generator_node_unavailable")
	}
	generatorRunner := &generator.Runner{Node: nodeRunner}
	if specsDir := config.SpecsDir(); specsDir != "" {
		generatorRunner.Specs = &rules.SpecStore{Dir: specsDir}
		nodeRunner.Specs = generatorRunner.Specs
		logger.Printf("event=generator_specs path=%q", specsDir)
	} else {
		logger.Printf("event=generator_specs_unavailable")
	}
	completer := &agent.Completer{
		Pack:       pack,
		Grammar:    pack.Grammar(),
		Generators: generatorRunner,
		Specs:      generatorRunner.Specs,
		History:    history.Load(os.Getenv("OFFLINE_COMPLETE_HISTORY")),
		Logger:     logger,
	}
	if dataDir, err := store.DefaultDir(); err != nil {
		logger.Printf("event=learning_disabled err=%q", err)
		fmt.Fprintf(os.Stderr, "offline-complete: learning disabled: %v\n", err)
	} else if err := ensureDataDir(dataDir); err != nil {
		logger.Printf("event=learning_disabled err=%q", err)
		fmt.Fprintf(os.Stderr, "offline-complete: learning disabled: %v\n", err)
	} else if learningStore, err := store.Open(dataDir); err != nil {
		logger.Printf("event=learning_disabled err=%q", err)
		fmt.Fprintf(os.Stderr, "offline-complete: learning disabled: %v\n", err)
	} else {
		completer.Ranker = &agent.StoreRanker{Store: learningStore, Logger: logger}
		stats := learningStore.Stats()
		logger.Printf("event=learning_enabled path=%q selections=%d", stats.Path, stats.Selections)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return runHooks{}, nil, err
	}
	base, err := config.SocketBase()
	if err != nil {
		return runHooks{}, nil, err
	}
	dataDir := filepath.Dir(base)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		logger.Printf("event=agent_start_error reason=socket_parent err=%q", err)
		return runHooks{}, nil, fmt.Errorf("prepare socket parent: %w", err)
	}
	// This is the application's own data directory. Tighten permissions left
	// by an older build; reject symlinks through the private-base check below.
	if info, statErr := os.Lstat(dataDir); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		_ = os.Chmod(dataDir, 0o700)
	}
	if err := ipc.EnsurePrivateBase(base); err != nil {
		logger.Printf("event=agent_start_error reason=socket err=%q", err)
		return runHooks{}, nil, fmt.Errorf("prepare socket directory: %w", err)
	}
	listener, err := ipc.NewPrivateListener(base)
	if err != nil {
		return runHooks{}, nil, fmt.Errorf("create socket: %w", err)
	}
	sessionID, err := ipc.NewSessionID()
	if err != nil {
		_ = listener.Close()
		return runHooks{}, nil, err
	}
	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		_ = listener.Close()
		return runHooks{}, nil, err
	}
	tempDir, err := os.MkdirTemp("", "offline-complete-")
	if err != nil {
		_ = listener.Close()
		_ = pipeR.Close()
		_ = pipeW.Close()
		return runHooks{}, nil, err
	}
	if err := os.Chmod(tempDir, 0o700); err != nil {
		_ = os.RemoveAll(tempDir)
		_ = listener.Close()
		_ = pipeR.Close()
		_ = pipeW.Close()
		return runHooks{}, nil, err
	}

	homeRC := filepath.Join(home, ".zshrc")
	hooksFile := filepath.Join(tempDir, "hooks.zsh")
	rcFile := filepath.Join(tempDir, ".zshrc")
	homeLiteral, _ := shellparse.ZshLiteral(homeRC)
	hooksLiteral, _ := shellparse.ZshLiteral(hooksFile)
	zshrc := "source " + hooksLiteral + "\n"
	if os.Getenv("OFFLINE_COMPLETE_NO_USER_RC") == "" {
		zshrc = "[[ -f " + homeLiteral + " ]] && source " + homeLiteral + "\n" + zshrc
	}
	if err := os.WriteFile(hooksFile, []byte(editstate.ZLETransportScript()), 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		_ = listener.Close()
		_ = pipeR.Close()
		_ = pipeW.Close()
		return runHooks{}, nil, err
	}
	if err := os.WriteFile(rcFile, []byte(zshrc), 0o600); err != nil {
		_ = os.RemoveAll(tempDir)
		_ = listener.Close()
		_ = pipeR.Close()
		_ = pipeW.Close()
		return runHooks{}, nil, err
	}

	runtime = &agentRuntime{
		listener:   listener,
		sessionID:  sessionID,
		state:      &editstate.State{},
		pipeR:      pipeR,
		pipeW:      pipeW,
		tempDir:    tempDir,
		stop:       make(chan struct{}),
		accept:     make(chan struct{}),
		generators: generatorRunner,
		logger:     logger,
		startedAt:  time.Now(),
	}
	logger.Printf("event=socket_ready session=%s socket=%q base=%q", sessionID, listener.Path(), base)
	if os.Getenv("OFFLINE_COMPLETE_AUTO") == "1" {
		if dir, err := config.DataDir(); err == nil {
			runtime.presencePath = filepath.Join(dir, "app.pid")
			go runtime.watchPresence()
		}
	}
	runtime.navigator = &navigator{can: runtime.hasOffer, on: runtime.navigate}
	if os.Getenv("OFFLINE_COMPLETE_NO_WINDOW") == "" {
		runtime.mu.Lock()
		disabled := runtime.disabled
		runtime.mu.Unlock()
		if !disabled {
			if windowPath := config.WindowPath(); windowPath != "" {
				window := windowCommand(windowPath, listener.Path(), sessionID)
				window.Stdout = os.Stderr
				window.Stderr = os.Stderr
				if err := window.Start(); err != nil {
					logger.Printf("event=window_error err=%q", err)
					fmt.Fprintf(os.Stderr, "offline-complete: window: %v\n", err)
				} else {
					runtime.window = window
					logger.Printf("event=window_start path=%q pid=%d", windowPath, window.Process.Pid)
					go func() {
						waitErr := window.Wait()
						logger.Printf("event=window_exit err=%q", waitErr)
					}()
				}
			}
		}
	}

	spawn := func(rows, cols uint16) (*session.Session, error) {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = append(os.Environ(), "ZDOTDIR="+tempDir, "OFFLINE_COMPLETE_ACTIVE=1")
		cmd.ExtraFiles = []*os.File{pipeW}
		started, err := session.SpawnCommand(cmd, rows, cols)
		// The parent must not keep the write end open, or EOF never arrives.
		_ = pipeW.Close()
		return started, err
	}

	started := func(s *session.Session, writer io.WriteCloser) error {
		if runtime.base == nil {
			return errors.New("missing session writer")
		}
		runtime.mu.Lock()
		disabled := runtime.disabled
		runtime.mu.Unlock()
		if disabled {
			close(runtime.accept)
			return nil
		}
		completion, err := agent.NewSession(sessionID, runtime.state, completer, runtime.base)
		if err != nil {
			return err
		}
		if completer.Ranker != nil {
			completion.SetRecorder(completer.Ranker)
		}
		go func() {
			_ = editstate.ConsumeNULNotify(runtime.pipeR, runtime.state, runtime.refresh)
		}()
		go runtime.acceptLoop(completion)
		logger.Printf("event=session_start session=%s shell=%q socket=%q", sessionID, filepath.Base(args[0]), listener.Path())
		if os.Getenv("OFFLINE_COMPLETE_QUIET") != "1" {
			fmt.Fprintf(os.Stderr, "offline-complete: session=%s socket=%s\n", sessionID, listener.Path())
		}
		return nil
	}

	return runHooks{spawn: spawn, started: started, cleanup: runtime.close}, runtime, nil
}

func windowCommand(path, socket, session string) *exec.Cmd {
	return exec.Command(path, "--socket", socket, "--session", session)
}

func ensureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("data path is not a directory")
	}
	return os.Chmod(dir, 0o700)
}

func presenceAlive(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (r *agentRuntime) watchPresence() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			if !presenceAlive(r.presencePath) {
				r.disableCompletion()
				return
			}
		}
	}
}

func (r *agentRuntime) disableCompletion() {
	r.mu.Lock()
	if r.disabled {
		r.mu.Unlock()
		return
	}
	r.disabled = true
	service := r.service
	r.service = nil
	window := r.window
	r.window = nil
	r.mu.Unlock()

	r.logger.Printf("event=app_stopped_completion session=%s", r.sessionID)
	if service != nil {
		_ = service.Close()
	}
	if window != nil && window.Process != nil {
		_ = window.Process.Kill()
	}
	if r.listener != nil {
		_ = r.listener.Close()
	}
}

func (r *agentRuntime) hasOffer() bool {
	r.mu.Lock()
	service := r.service
	r.mu.Unlock()
	return service != nil && service.HasOffer()
}

func (r *agentRuntime) navigate(action agent.Navigation) bool {
	r.mu.Lock()
	service := r.service
	r.mu.Unlock()
	if service == nil {
		return false
	}
	return service.Navigate(action)
}

func (r *agentRuntime) refresh() {
	r.mu.Lock()
	service := r.service
	r.mu.Unlock()
	if service != nil {
		service.Refresh()
	}
}

func (r *agentRuntime) acceptLoop(completion *agent.Session) {
	defer close(r.accept)
	for {
		r.mu.Lock()
		disabled := r.disabled
		r.mu.Unlock()
		if disabled {
			return
		}
		select {
		case <-r.stop:
			return
		default:
		}
		conn, err := ipc.AcceptAuthenticated(r.listener.Listener(), r.sessionID, 5*time.Second)
		if err != nil {
			select {
			case <-r.stop:
				return
			default:
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		service, err := agent.NewService(completion, r.state, conn)
		if err != nil {
			r.logger.Printf("event=interface_rejected err=%q", err)
			_ = conn.Close()
			continue
		}
		service.Logger = r.logger
		r.logger.Printf("event=interface_connected")
		r.mu.Lock()
		previous := r.service
		r.service = service
		r.mu.Unlock()
		if previous != nil {
			_ = previous.Close()
		}
		service.Refresh()
		serveErr := service.Serve()
		if serveErr != nil && !errors.Is(serveErr, io.EOF) && !errors.Is(serveErr, net.ErrClosed) {
			r.logger.Printf("event=interface_closed err=%q", serveErr)
		} else {
			r.logger.Printf("event=interface_closed")
		}
		_ = service.Close()
		r.mu.Lock()
		if r.service == service {
			r.service = nil
		}
		r.mu.Unlock()
	}
}

func (r *agentRuntime) close() {
	r.stopOnce.Do(func() { close(r.stop) })
	r.logger.Printf("event=agent_stop session=%s uptime_ms=%d", r.sessionID, time.Since(r.startedAt).Milliseconds())
	if r.listener != nil {
		_ = r.listener.Close()
	}
	r.mu.Lock()
	service := r.service
	r.service = nil
	r.mu.Unlock()
	if service != nil {
		_ = service.Close()
	}
	if r.pipeR != nil {
		_ = r.pipeR.Close()
	}
	if r.pipeW != nil {
		_ = r.pipeW.Close()
	}
	r.state.Close()
	if r.window != nil {
		_ = r.window.Process.Kill()
	}
	if r.generators != nil && r.generators.Node != nil {
		r.generators.Node.Close()
	}
	if r.tempDir != "" {
		_ = os.RemoveAll(r.tempDir)
	}
	select {
	case <-r.accept:
	case <-time.After(time.Second):
	}
	_ = r.logger.Close()
}
