import { existsSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { parseDocument } from "yaml";

const [templatePath, configPath, port] = process.argv.slice(2);
if (!templatePath || !configPath || !/^[1-9][0-9]{0,4}$/.test(port ?? "") || Number(port) > 65535) {
  throw new Error("Usage: prepare-dev-config.mjs TEMPLATE CONFIG PORT (1-65535)");
}

const creating = !existsSync(configPath);
const document = parseDocument(readFileSync(creating ? templatePath : configPath, "utf8"));
if (document.errors.length > 0) {
  throw new Error(`Invalid development configuration: ${document.errors[0].message}`);
}

// The launcher owns the development listener. Keep all other edits on restarts.
document.setIn(["server", "listen"], `127.0.0.1:${port}`);
if (creating) {
  document.setIn(["storage", "data_dir"], "./data-dev");
}

const temporaryPath = `${configPath}.${process.pid}.tmp`;
try {
  writeFileSync(temporaryPath, document.toString(), { mode: 0o600, flag: "wx" });
  renameSync(temporaryPath, configPath);
} finally {
  rmSync(temporaryPath, { force: true });
}
