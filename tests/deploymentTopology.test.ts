import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

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
