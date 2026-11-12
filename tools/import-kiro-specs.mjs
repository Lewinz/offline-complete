#!/usr/bin/env node
// Import Kiro/Fig autocomplete specs into the bounded static rule pack. The
// raw spec modules are copied alongside the pack so the runtime can execute
// their generators (custom JavaScript, postProcess, templates and loadSpec).
//
// Usage: node tools/import-kiro-specs.mjs MANIFEST SPECS_DIR OUTPUT_JSON
import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { pathToFileURL } from "node:url";

const manifestPath = process.argv[2] ?? "/tmp/kiro-spec-manifest.json";
const specsDir = process.argv[3] ?? "rules/specs";
const output = process.argv[4] ?? "rules/base.json";
const manifest = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
const manifestByName = new Map(manifest.map((entry) => [entry.name, entry]));

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

function description(value) {
  return typeof value === "string" ? value.trim() : "";
}

function generators(value) {
  const result = [];
  for (const item of items(value)) {
    if (!item || typeof item !== "object") continue;
    if (Array.isArray(item.script) && item.script.length && item.script.every((part) => typeof part === "string" && part && !/[\x00-\x1f]/.test(part))) {
      result.push({ script: item.script });
    }
    if (item.template !== undefined) {
      const template = typeof item.template === "string" ? [item.template] : item.template;
      if (Array.isArray(template) && template.length && template.every((part) => typeof part === "string" && part && !/[\x00-\x1f]/.test(part))) {
        result.push({ template });
      }
    }
  }
  return result;
}

function suggestions(value) {
  const result = [];
  for (const item of items(value)) {
    if (!item || typeof item !== "object" || item.hidden) continue;
    const name = first(item.name);
    if (!name) continue;
    result.push({ name, description: description(item.description) });
  }
  return result;
}

function args(value) {
  const result = [];
  for (const item of items(value)) {
    if (!item || typeof item !== "object") continue;
    const arg = { name: first(item.name), description: description(item.description) };
    const generated = generators(item.generators);
    if (generated.length) arg.generators = generated;
    const suggested = suggestions(item.suggestions);
    if (suggested.length) arg.suggestions = suggested;
    if (item.isVariadic) arg.isVariadic = true;
    if (item.isOptional) arg.isOptional = true;
    if (typeof item.filterStrategy === "string") arg.filterStrategy = item.filterStrategy;
    if (arg.name || arg.generators || arg.suggestions) result.push(arg);
  }
  return result;
}

function convertOption(raw, seen, depth) {
  if (!raw || typeof raw !== "object" || seen.has(raw) || depth > 40) return null;
  seen.add(raw);
  const name = first(raw.name);
  if (!name) {
    seen.delete(raw);
    return null;
  }
  const option = { name, description: description(raw.description) };
  const optionArgs = args(raw.args);
  if (optionArgs.length) option.args = optionArgs;
  seen.delete(raw);
  return option;
}

function convertNode(node, seen, depth) {
  if (!node || typeof node !== "object" || seen.has(node) || depth > 40) return null;
  seen.add(node);
  const name = first(node.name);
  if (!name) {
    seen.delete(node);
    return null;
  }
  const command = { name, description: description(node.description) };
  if (node.loadSpec !== undefined || node.generateSpec !== undefined) command.dynamic = true;
  const options = [];
  const seenOptions = new Set();
  for (const key of ["options", "persistentOptions"]) {
    for (const raw of list(node[key])) {
      const option = convertOption(raw, seen, depth + 1);
      if (option && !seenOptions.has(option.name)) {
        seenOptions.add(option.name);
        options.push(option);
      }
    }
  }
  if (options.length) command.options = options;
  const subcommands = [];
  const seenSubcommands = new Set();
  for (const raw of list(node.subcommands)) {
    const child = convertNode(raw, seen, depth + 1);
    if (child && !seenSubcommands.has(child.name)) {
      seenSubcommands.add(child.name);
      subcommands.push(child);
    }
  }
  if (subcommands.length) command.subcommands = subcommands;
  const positional = args(node.args);
  if (positional.length) command.args = positional;
  seen.delete(node);
  return command;
}

function specID(name) {
  return crypto.createHash("sha256").update(name).digest("hex").slice(0, 16);
}

function rewriteAI(source) {
  return source
    .replaceAll(
      'command:"fig",args:["settings","--format","json","autocomplete.ai.enabled"]',
      'command:"oc",args:["setting","autocomplete.ai.enabled"]',
    )
    .replaceAll(
      'command:"fig",args:["_","request","--route","/ai/chat","--method","POST","--body"',
      'command:"oc",args:["ai","request","--method","POST","--body"',
    );
}

fs.mkdirSync(specsDir, { recursive: true });
const specIndex = {};
for (const entry of manifest) {
  if (!entry.path) continue;
  const id = specID(entry.name);
  fs.writeFileSync(path.join(specsDir, `${id}.js`), rewriteAI(fs.readFileSync(entry.path, "utf8")));
  specIndex[entry.name] = id;
}
fs.writeFileSync(path.join(specsDir, "index.json"), JSON.stringify(specIndex));
const commands = [];
const specs = {};
const seenCommands = new Set();
let importedSpecs = 0;
for (const entry of manifest) {
  if (!entry.path || entry.name.includes("/") || entry.name.includes("@")) continue;
  try {
    let sourceEntry = entry;
    let source = rewriteAI(fs.readFileSync(sourceEntry.path, "utf8"));
    const module = await import(pathToFileURL(sourceEntry.path).href);
    let spec = module.default;
    if (typeof spec === "function") spec = await spec();
    if (spec?.versionedSpecPath) {
      const versioned = manifestByName.get(spec.versionedSpecPath);
      if (!versioned?.path) continue;
      sourceEntry = versioned;
      source = rewriteAI(fs.readFileSync(sourceEntry.path, "utf8"));
      const versionedModule = await import(pathToFileURL(sourceEntry.path).href);
      spec = versionedModule.default;
      if (typeof spec === "function") spec = await spec();
    }
    if (!spec || typeof spec !== "object" || spec?.versions) continue;
    const command = convertNode(spec, new Set(), 0);
    if (!command || command.name.includes("/") || command.name.includes("@") || seenCommands.has(command.name)) continue;
    const id = specID(sourceEntry.name);
    fs.writeFileSync(path.join(specsDir, `${id}.js`), source);
    specs[command.name] = id;
    seenCommands.add(command.name);
    commands.push(command);
    importedSpecs++;
  } catch (error) {
    // A missing or unparseable upstream spec must not silently become a
    // partial command: skip it and let the source manifest remain the record.
  }
}

const pack = { schemaVersion: 1, commands, specs };
fs.writeFileSync(output, JSON.stringify(pack));
function walk(node) {
  let count = 1;
  for (const option of node.options ?? []) count += 1 + (option.args?.length ?? 0);
  for (const subcommand of node.subcommands ?? []) count += walk(subcommand);
  return count;
}
console.log(JSON.stringify({
  commands: commands.length,
  specs: importedSpecs,
  nodes: commands.reduce((sum, command) => sum + walk(command), 0),
  bytes: fs.statSync(output).size,
}));
