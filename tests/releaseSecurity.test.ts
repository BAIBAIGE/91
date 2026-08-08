import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const packageJson = JSON.parse(
  readFileSync(new URL("../package.json", import.meta.url), "utf8")
) as {
  allowScripts?: Record<string, boolean>;
  engines?: Record<string, string>;
  scripts?: Record<string, string>;
};
const dockerfile = readFileSync(
  new URL("../Dockerfile", import.meta.url),
  "utf8"
);
const dockerWorkflow = readFileSync(
  new URL("../.github/workflows/docker-build.yml", import.meta.url),
  "utf8"
);
const releaseWorkflow = readFileSync(
  new URL("../.github/workflows/release.yml", import.meta.url),
  "utf8"
);
const releaseScript = readFileSync(
  new URL("../scripts/build-release.sh", import.meta.url),
  "utf8"
);

test("frontend verification rejects every npm audit finding before release", () => {
  assert.deepEqual(packageJson.allowScripts, {
    "esbuild@0.28.1": true,
    "fsevents@2.3.3": true,
  });
  assert.equal(packageJson.scripts?.audit, "npm audit --audit-level=info");
  assert.equal(
    packageJson.scripts?.check,
    "npm run audit && npm run lint && npm test"
  );
  assert.equal(packageJson.scripts?.verify, "npm run check && npm run build");
  assert.match(
    releaseScript,
    /npm --prefix "\$ROOT_DIR" run verify/
  );
});

test("container and release builds use supported Node and run checks", () => {
  assert.equal(packageJson.engines?.node, ">=22.12.0");
  assert.match(dockerfile, /^FROM node:24-slim AS frontend$/m);
  assert.match(dockerfile, /^COPY package\.json package-lock\.json \.npmrc \.\/$/m);
  assert.match(dockerWorkflow, /node-version: "24"/);
  assert.match(releaseWorkflow, /node-version: "24"/);
  assert.match(
    dockerWorkflow,
    /run: npm run check[\s\S]*?uses: docker\/build-push-action@v6/
  );
});
