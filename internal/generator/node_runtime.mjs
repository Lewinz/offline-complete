// Long-lived generator runtime for Offline Complete.
//
// The Go agent sends one JSON request per line. Each request carries the source
// of one Kiro/Fig spec module and the completion context. This process evaluates
// the module in an isolated data: URL, walks to the requested argument and runs
// its generator. It never invokes a shell: commands are spawned as argv arrays.
//
// This is intentionally the executable half of the rule pack. The Go side owns
// parsing, timeouts, validation and insertion; this side reproduces the
// generator semantics that cannot be represented as static JSON.
import fs from "node:fs";
import path from "node:path";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { createInterface } from "node:readline";

const modules = new Map();
const caches = new Map();
const pendingSpecs = new Map();
let nextSpecRequest = 0;
const DEFAULT_TIMEOUT = 700;
const MAX_OUTPUT = 256 * 1024;
const MAX_CANDIDATES = 100;

function first(value) {
  if (Array.isArray(value)) {
    for (const item of value) if (typeof item === "string" && item.trim()) return item.trim();
  } else if (typeof value === "string") {
    return value.trim();
  }
  return "";
}

function list(value) {
  if (Array.isArray(value)) return value;
  if (value && typeof value === "object") return Object.values(value);
  return [];
}

function items(value) {
  if (Array.isArray(value)) return value;
  if (value && typeof value === "object") return [value];
  return [];
}

function nameMatches(value, wanted) {
  if (Array.isArray(value)) return value.some((item) => item === wanted);
  return value === wanted;
}

function findChild(nodes, name) {
  for (const node of list(nodes)) if (node && nameMatches(node.name, name)) return node;
  return undefined;
}

function mergeCollection(target, extra) {
  const values = items(extra);
  if (!values.length) return target;
  if (Array.isArray(target)) return target.concat(values);
  if (target && typeof target === "object") {
    const result = { ...target };
    for (const value of values) result[first(value.name)] = value;
    return result;
  }
  return values;
}

function mergeSpec(target, extra) {
  if (!target || !extra || typeof extra !== "object") return target;
  if (Array.isArray(extra)) {
    for (const item of extra) mergeSpec(target, item);
    return target;
  }
  if (extra.subcommands) target.subcommands = mergeCollection(target.subcommands, extra.subcommands);
  if (extra.options) target.options = mergeCollection(target.options, extra.options);
  if (extra.persistentOptions) target.persistentOptions = mergeCollection(target.persistentOptions, extra.persistentOptions);
  if (extra.args && (!target.args || !items(target.args).length)) target.args = extra.args;
  return target;
}

function requestSpec(name) {
  return new Promise((resolve, reject) => {
    const requestId = ++nextSpecRequest;
    pendingSpecs.set(requestId, { resolve, reject });
    process.stdout.write(JSON.stringify({ type: "spec-request", requestId, name }) + "\n");
  });
}

async function materialize(source) {
  const module = await loadModule(source);
  let spec = module.default;
  if (typeof spec === "function") spec = await spec();
  if (spec?.versionedSpecPath) {
    const versioned = await requestSpec(spec.versionedSpecPath);
    return materialize(versioned);
  }
  return spec;
}

async function resolveSpec(value, context) {
  let result = value;
  if (typeof result === "function") {
    try {
      result = await result(context.tokens, (options) => executeCommand(options, context), context.generatorContext);
    } catch {
      return undefined;
    }
  }
  if (typeof result === "string") {
    try {
      return await materialize(await requestSpec(result));
    } catch {
      return undefined;
    }
  }
  return result;
}

async function augmentNode(node, context) {
  if (!node || typeof node !== "object") return node;
  if (typeof node.generateSpec === "function") {
    try {
      const generated = await node.generateSpec(context.tokens, (options) => executeCommand(options, context));
      if (generated) mergeSpec(node, generated);
    } catch { /* keep the static tree */ }
  }
  if (node.loadSpec !== undefined) {
    try {
      const loaded = await resolveSpec(node.loadSpec, context);
      if (loaded) mergeSpec(node, loaded);
    } catch { /* keep the static tree */ }
  }
  return node;
}

async function findNode(spec, commandPath, context) {
  let node = await augmentNode(spec, context);
  for (const part of commandPath.slice(1)) {
    let child = findChild(node?.subcommands, part);
    if (!child && node?.loadSpec !== undefined) {
      const loaded = await resolveSpec(node.loadSpec, context);
      if (loaded) {
        mergeSpec(node, loaded);
        child = findChild(node.subcommands, part);
      }
    }
    if (!child) return undefined;
    node = await augmentNode(child, context);
  }
  return node;
}

function argAt(value, slot) {
  const args = items(value);
  if (!args.length || slot < 0) return undefined;
  if (slot < args.length) return args[slot];
  const last = args[args.length - 1];
  return last?.isVariadic ? last : undefined;
}

function findOption(node, name) {
  for (const option of list(node?.options)) {
    if (option && nameMatches(option.name, name)) return option;
  }
  return undefined;
}

function isGenerator(value) {
  return Boolean(value && typeof value === "object" && (
    value.script !== undefined ||
    value.custom !== undefined ||
    value.template !== undefined ||
    value.postProcess !== undefined ||
    value.splitOn !== undefined
  ));
}

function generatorList(arg) {
  if (!arg) return [];
  const value = arg.generators;
  if (Array.isArray(value)) return value.filter(isGenerator);
  if (isGenerator(value)) return [value];
  return list(value).filter(isGenerator);
}

function currentToken(tokens, prefix) {
  if (prefix) return prefix;
  return tokens.length ? tokens[tokens.length - 1] : "";
}

function cacheKey(generator, context) {
  if (generator.cache?.cacheKey) return `${generator.cache.cacheKey}\0${context.currentWorkingDirectory}`;
  return JSON.stringify({
    script: typeof generator.script === "function" ? String(generator.script) : generator.script,
    template: generator.template,
    cwd: context.currentWorkingDirectory,
  });
}

function getCache(key, ttl) {
  const entry = caches.get(key);
  if (!entry) return undefined;
  if (Date.now() - entry.created > ttl) {
    caches.delete(key);
    return undefined;
  }
  return entry.value;
}

function setCache(key, value) {
  caches.set(key, { created: Date.now(), value });
}

function limitText(text) {
  if (typeof text !== "string") return "";
  return text.length > MAX_OUTPUT ? text.slice(0, MAX_OUTPUT) : text;
}

function aiProtocol() {
  const configured = (process.env.OFFLINE_COMPLETE_AI_PROTOCOL || "").toLowerCase();
  if (configured === "openai" || configured === "anthropic") return configured;
  const endpoint = process.env.OFFLINE_COMPLETE_AI_URL || "";
  if (/\/chat\/completions(?:\?|$)/.test(endpoint)) return "openai";
  if (/\/messages(?:\?|$)/.test(endpoint)) return "anthropic";
  return "";
}

async function ocCommand(args) {
  if ((args[0] === "settings" || args[0] === "setting") && args.includes("autocomplete.ai.enabled")) {
    const enabled = process.env.OFFLINE_COMPLETE_AI_ENABLED !== "0" && Boolean(process.env.OFFLINE_COMPLETE_AI_URL && aiProtocol());
    return { stdout: JSON.stringify(enabled), stderr: "", code: 0 };
  }
  if (!(args[0] === "ai" && args[1] === "request")) {
    return { stdout: "", stderr: "unsupported oc command", code: 1 };
  }
  const endpoint = process.env.OFFLINE_COMPLETE_AI_URL;
  const protocol = aiProtocol();
  if (process.env.OFFLINE_COMPLETE_AI_ENABLED === "0" || !endpoint || !protocol) return { stdout: "", stderr: "AI endpoint or protocol is not configured", code: 1 };
  let method = "POST";
  let body = "";
  for (let i = 2; i < args.length; i++) {
    if (args[i] === "--method") method = args[++i] ?? "POST";
    else if (args[i] === "--body") body = args[++i] ?? "";
  }
  let parsed = {};
  try { parsed = body ? JSON.parse(body) : {}; } catch { parsed = {}; }
  const messages = Array.isArray(parsed.messages) ? parsed.messages : [];
  let payload;
  const headers = { "content-type": "application/json" };
  if (protocol === "openai") {
    if (process.env.OFFLINE_COMPLETE_AI_KEY) headers.authorization = `Bearer ${process.env.OFFLINE_COMPLETE_AI_KEY}`;
    payload = { ...parsed };
    delete payload.source;
    delete payload.name;
    if (process.env.OFFLINE_COMPLETE_AI_MODEL) payload.model = process.env.OFFLINE_COMPLETE_AI_MODEL;
    if (!payload.model) payload.model = "gpt-4o-mini";
  } else {
    if (process.env.OFFLINE_COMPLETE_AI_KEY) headers["x-api-key"] = process.env.OFFLINE_COMPLETE_AI_KEY;
    headers["anthropic-version"] = "2023-06-01";
    const system = messages.filter((item) => item.role === "system").map((item) => item.content).join("\n");
    payload = {
      model: process.env.OFFLINE_COMPLETE_AI_MODEL || "claude-3-5-sonnet-latest",
      max_tokens: Number(process.env.OFFLINE_COMPLETE_AI_MAX_TOKENS) || 1024,
      messages: messages.filter((item) => item.role !== "system").map((item) => ({ role: item.role, content: item.content })),
    };
    if (system) payload.system = system;
    if (typeof parsed.temperature === "number") payload.temperature = parsed.temperature;
  }
  try {
    const response = await fetch(endpoint, { method, headers, body: JSON.stringify(payload) });
    const text = await response.text();
    if (protocol === "anthropic" && response.ok) {
      try {
        const data = JSON.parse(text);
        const content = (data.content || []).filter((item) => item.type === "text").map((item) => item.text).join("\n");
        return { stdout: JSON.stringify({ choices: [{ message: { content } }] }), stderr: "", code: 0 };
      } catch {
        return { stdout: "", stderr: "invalid Anthropic response", code: 1 };
      }
    }
    return { stdout: text, stderr: "", code: response.ok ? 0 : response.status };
  } catch (error) {
    return { stdout: "", stderr: String(error), code: 1 };
  }
}
function executeCommand(options, context) {
  const command = Array.isArray(options?.command)
    ? options.command
    : typeof options?.command === "string"
      ? [options.command]
      : Array.isArray(options)
        ? options
        : [];
  if (!command.length || typeof command[0] !== "string") return Promise.resolve({ stdout: "", stderr: "", code: 1 });
  const args = Array.isArray(options?.args)
    ? options.args
    : command.slice(1);
  const executable = command[0];
  if (executable === "oc") return ocCommand(args);
  const cwd = typeof options?.cwd === "string" ? options.cwd : context.currentWorkingDirectory;
  const timeout = Math.max(50, Math.min(Number(options?.timeout) || 700, 5000));
  return new Promise((resolve) => {
    let settled = false;
    let stdout = "";
    let stderr = "";
    let child;
    const finish = (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({ stdout: limitText(stdout), stderr: limitText(stderr), code });
    };
    const timer = setTimeout(() => {
      try { child?.kill("SIGKILL"); } catch { /* already gone */ }
      finish(124);
    }, timeout);
    try {
      child = spawn(executable, args.map(String), {
        cwd,
        env: { ...process.env, GIT_OPTIONAL_LOCKS: "0" },
        shell: false,
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (error) {
      stderr = String(error);
      finish(1);
      return;
    }
    child.stdout?.on("data", (chunk) => {
      if (stdout.length < MAX_OUTPUT) stdout += chunk.toString("utf8").slice(0, MAX_OUTPUT - stdout.length);
    });
    child.stderr?.on("data", (chunk) => {
      if (stderr.length < MAX_OUTPUT) stderr += chunk.toString("utf8").slice(0, MAX_OUTPUT - stderr.length);
    });
    child.on("error", (error) => { stderr = String(error); finish(1); });
    child.on("close", (code) => finish(code ?? 0));
  });
}

function normalizeCandidate(value) {
  if (typeof value === "string") {
    const name = value.trim();
    return name ? { name } : undefined;
  }
  if (!value || typeof value !== "object") return undefined;
  let name = first(value.name ?? value.insertValue);
  if (!name && typeof value.displayName === "string") name = value.displayName.trim();
  if (!name) return undefined;
  const candidate = { name };
  if (typeof value.description === "string" && value.description.trim()) candidate.description = value.description.trim();
  if (typeof value.insertValue === "string" && value.insertValue.trim()) candidate.insertValue = value.insertValue.trim();
  return candidate;
}

function normalizeList(value) {
  const result = [];
  for (const item of items(value)) {
    const candidate = normalizeCandidate(item);
    if (candidate) result.push(candidate);
    if (result.length >= MAX_CANDIDATES) break;
  }
  return result;
}

function matches(name, query, fuzzy) {
  if (!query || name.startsWith(query)) return true;
  if (!fuzzy) return false;
  const needle = query.toLowerCase();
  const haystack = name.toLowerCase();
  let index = 0;
  for (const char of haystack) {
    if (index < needle.length && char === needle[index]) index++;
  }
  return index === needle.length;
}

function splitOutput(output, splitOn) {
  if (typeof splitOn === "string" && splitOn.length) {
    return output.trim() ? output.trim().split(splitOn) : [];
  }
  return output.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

function queryTerm(generator, token, context) {
  try {
    if (typeof generator.getQueryTerm === "function") {
      const value = generator.getQueryTerm(token, context);
      return typeof value === "string" ? value : token;
    }
  } catch { /* fall through */ }
  return token;
}

function shouldRun(generator, token, previousToken, context) {
  const trigger = generator.trigger;
  if (trigger === undefined) return true;
  try {
    if (typeof trigger === "function") return Boolean(trigger(token, previousToken, context));
    if (typeof trigger === "string") return token.includes(trigger);
    if (Array.isArray(trigger)) return trigger.some((item) => token.includes(String(item)));
  } catch { /* skip a broken trigger */ }
  return false;
}

function templateCandidates(template, token, cwd) {
  const values = items(template).filter((item) => typeof item === "string");
  if (!values.length) return [];
  const foldersOnly = values.length === 1 && values[0] === "folders";
  const filesOnly = values.length === 1 && values[0] === "filepaths";
  const split = token.lastIndexOf("/");
  const dirPart = split >= 0 ? token.slice(0, split + 1) : "";
  const query = split >= 0 ? token.slice(split + 1) : token;
  const directory = path.isAbsolute(dirPart) ? dirPart : path.resolve(cwd || process.cwd(), dirPart);
  let entries;
  try {
    entries = fs.readdirSync(directory, { withFileTypes: true });
  } catch {
    return [];
  }
  entries.sort((a, b) => a.name.localeCompare(b.name));
  const result = [];
  for (const entry of entries) {
    if (entry.name.startsWith(".") && !query.startsWith(".")) continue;
    if (!entry.name.startsWith(query)) continue;
    if (foldersOnly && !entry.isDirectory()) continue;
    if (filesOnly && !entry.isFile() && !entry.isDirectory()) continue;
    const name = dirPart + entry.name + (entry.isDirectory() ? "/" : "");
    result.push({ name, description: entry.isDirectory() ? "目录" : "文件" });
    if (result.length >= MAX_CANDIDATES) break;
  }
  return result;
}

async function runOne(generator, rawToken, query, previousToken, context) {
  if (!shouldRun(generator, rawToken, previousToken, context)) return [];
  if (generator.template !== undefined) {
    return templateCandidates(generator.template, rawToken, context.currentWorkingDirectory);
  }
  if (generator.custom) {
    try {
      const generatorContext = { ...context.generatorContext, searchTerm: rawToken };
      const value = await generator.custom(context.tokens, (options) => executeCommand(options, context), generatorContext);
      return normalizeList(value);
    } catch {
      return [];
    }
  }
  if (generator.script) {
    const ttl = Number(generator.cache?.ttl) || 0;
    const key = cacheKey(generator, context);
    if (ttl > 0) {
      const cached = getCache(key, ttl);
      if (cached) return cached;
    }
    let script = generator.script;
    try {
      if (typeof script === "function") script = await script(context.tokens);
    } catch {
      return [];
    }
    let options;
    if (Array.isArray(script)) options = { command: script[0], args: script.slice(1), cwd: context.currentWorkingDirectory };
    else if (script && typeof script === "object") options = script;
    if (!options || !options.command) return [];
    const timeout = Math.max(DEFAULT_TIMEOUT, Number(generator.scriptTimeout) || 0, Number(options.timeout) || 0);
    const output = await executeCommand({ ...options, timeout }, context);
    let values;
    if (generator.postProcess) {
      try {
        values = await generator.postProcess(output.stdout, context.tokens);
      } catch {
        return [];
      }
    } else {
      values = splitOutput(output.stdout, generator.splitOn);
    }
    const result = normalizeList(values);
    if (ttl > 0) setCache(key, result);
    return result;
  }
  return [];
}

async function loadModule(source) {
  const digest = createHash("sha256").update(source).digest("hex");
  if (modules.has(digest)) return modules.get(digest);
  const url = `data:text/javascript;base64,${Buffer.from(source, "utf8").toString("base64")}#${digest}`;
  const module = await import(url);
  modules.set(digest, module);
  return module;
}

async function complete(request) {
  const module = await loadModule(request.source);
  const spec = module.default;
  if (!spec || typeof spec !== "object") throw new Error("spec has no default object");
  const tokens = Array.isArray(request.tokens) ? request.tokens.map(String) : [];
  const token = currentToken(tokens, request.prefix ?? "");
  const previousToken = tokens.length > 1 ? tokens[tokens.length - 2] : "";
  const context = {
    tokens,
    tokenArray: tokens,
    currentWorkingDirectory: request.cwd || process.cwd(),
    generatorContext: {
      currentWorkingDirectory: request.cwd || process.cwd(),
      currentProcess: "zsh",
      tokenArray: tokens,
      searchTerm: token,
      environmentVariables: process.env,
      sshPrefix: "",
      isDangerous: false,
    },
  };
  const node = await findNode(spec, [request.command, ...(request.subcommands ?? [])], context);
  if (!node) throw new Error(`spec node not found: ${[request.command, ...(request.subcommands ?? [])].join(" ")}`);
  if (request.kind === "subcommand" || request.kind === "option") {
    const source = request.kind === "subcommand" ? node.subcommands : node.options;
    const used = new Set((request.usedOptions ?? []).map(String));
    const result = [];
    for (const item of list(source)) {
      const name = first(item?.name);
      if (!name || used.has(name)) continue;
      if (!matches(name, request.prefix ?? "", false)) continue;
      const candidate = { name };
      if (typeof item.description === "string" && item.description.trim()) candidate.description = item.description.trim();
      result.push(candidate);
      if (result.length >= MAX_CANDIDATES) break;
    }
    return result;
  }
  let arg;
  if (request.kind === "option-value") {
    const option = findOption(node, request.option);
    arg = argAt(option?.args, Number(request.optionSlot) || 0);
  } else {
    arg = argAt(node.args, Number(request.slot) || 0);
    if (!arg && request.kind === "positional") {
      const prefix = request.prefix ?? "";
      const source = prefix.startsWith("-") ? node.options : node.subcommands;
      const result = [];
      for (const item of list(source)) {
        const name = first(item?.name);
        if (!name || !matches(name, prefix, false)) continue;
        const candidate = { name };
        if (typeof item.description === "string" && item.description.trim()) candidate.description = item.description.trim();
        result.push(candidate);
        if (result.length >= MAX_CANDIDATES) break;
      }
      return result;
    }
  }
  const generators = generatorList(arg);
  const candidates = [];
  const seen = new Set();
  for (const generator of generators) {
    const query = queryTerm(generator, token, context);
    const queryPrefix = query && token.endsWith(query) ? token.slice(0, token.length - query.length) : "";
    for (const candidate of await runOne(generator, token, query, previousToken, context)) {
      if (!candidate.name || seen.has(candidate.name)) continue;
      if (!matches(candidate.name, query, request.filterStrategy === "fuzzy")) continue;
      if (queryPrefix && !candidate.name.startsWith(queryPrefix)) {
        candidate.name = queryPrefix + candidate.name;
        if (candidate.insertValue) candidate.insertValue = queryPrefix + candidate.insertValue;
      }
      seen.add(candidate.name);
      candidates.push(candidate);
      if (candidates.length >= MAX_CANDIDATES) break;
    }
    if (candidates.length >= MAX_CANDIDATES) break;
  }
  return candidates;
}

const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
lines.on("line", async (line) => {
  let request;
  try {
    request = JSON.parse(line);
    if (request.type === "spec-response") {
      const pending = pendingSpecs.get(request.requestId);
      if (!pending) return;
      pendingSpecs.delete(request.requestId);
      if (request.error) pending.reject(new Error(request.error));
      else pending.resolve(request.source ?? "");
      return;
    }
    const candidates = await complete(request);
    process.stdout.write(JSON.stringify({ id: request.id, candidates }) + "\n");
  } catch (error) {
    process.stdout.write(JSON.stringify({ id: request?.id ?? 0, error: String(error?.message ?? error) }) + "\n");
  }
});
