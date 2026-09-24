import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const deploy = readFileSync(new URL("../deploy.sh", import.meta.url), "utf8");

test("systemd deployment serves built frontend assets from the backend", () => {
  assert.match(
    deploy,
    /BACKEND_LISTEN="\$\{BACKEND_LISTEN:-0\.0\.0\.0:\$\{FRONTEND_PORT\}\}"/
  );
  assert.match(deploy, /npm run build/);
  assert.doesNotMatch(deploy, /npm run preview/);
  assert.match(deploy, /ExecStart=\$\{REPO_DIR\}\/backend\/server/);
  assert.match(deploy, /retire_legacy_frontend_service/);
  assert.match(
    deploy,
    /systemctl disable --now "\$\{FRONTEND_SERVICE\}\.service"[\s\S]*?rm -f "\$frontend_unit"/
  );
  assert.match(deploy, /systemctl enable "\$\{BACKEND_SERVICE\}\.service"/);
  assert.doesNotMatch(
    deploy,
    /systemctl enable "\$\{BACKEND_SERVICE\}\.service" "\$\{FRONTEND_SERVICE\}\.service"/
  );
});

test("systemd deployment migrates only the legacy default listen address", () => {
  assert.match(deploy, /BACKEND_LISTEN_WAS_SET/);
  assert.match(deploy, /migrating legacy two-service listen address/);
  assert.match(deploy, /127\\\.0\\\.0\\\.1:9192/);
  assert.match(deploy, /backend\/config\.yaml already exists; keeping it/);
  assert.match(
    deploy,
    /install_frontend\s+build_backend[\s\S]*?prepare_config\s+write_systemd_units/
  );
});

test("source deployment creates Telegram configuration once and preserves it across upgrades", (t) => {
  const root = mkdtempSync(join(tmpdir(), "91-source-telegram-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  mkdirSync(join(root, "backend"));
  writeFileSync(join(root, "package.json"), "{}");
  writeFileSync(join(root, "backend", "config.example.yaml"), 'server:\n  listen: "0.0.0.0:9191"\n');
  const template = join(root, "telegram.example.yml");
  const example = readFileSync(new URL("../telegram.example.yml", import.meta.url), "utf8");
  writeFileSync(template, example);
  const deployScript = fileURLToPath(new URL("../deploy.sh", import.meta.url));
  const update = () => {
    const result = spawnSync("bash", ["-c", `
      source "$1" help >/dev/null
      REPO_DIR="$2"
      detect_deploy_user() { :; }
      install_dependencies() { :; }
      install_frontend() { :; }
      build_backend() { :; }
      ensure_ownership() { :; }
      write_systemd_units() { :; }
      open_firewall_port() { :; }
      restart_services() { :; }
      show_status() { :; }
      install_or_update update
    `, "bash", deployScript, root], { encoding: "utf8" });
    assert.equal(result.status, 0, result.stderr);
  };
  update();
  const compose = join(root, "backend", "telegram.yml");
  assert.equal(readFileSync(compose, "utf8"), example);
  assert.equal(statSync(compose).mode & 0o777, 0o600);
  const customized = example
    .replace('TELEGRAM_API_ID: ""', 'TELEGRAM_API_ID: "12345"')
    .replace('TELEGRAM_API_HASH: ""', 'TELEGRAM_API_HASH: "0123456789abcdef0123456789abcdef"')
    .replace("aiogram/telegram-bot-api:latest", "aiogram/telegram-bot-api:pinned")
    .replace("./data/telegram:/var/lib/telegram-bot-api", "/nzb/tg:/var/lib/telegram-bot-api");
  writeFileSync(compose, customized);
  writeFileSync(template, `# Updated release template\n${example}`);
  update();
  assert.equal(readFileSync(compose, "utf8"), customized);
});
