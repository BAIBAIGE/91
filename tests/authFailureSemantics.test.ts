import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const authContextSource = readFileSync(
  new URL("../src/admin/AuthContext.tsx", import.meta.url),
  "utf8"
);
const requireAuthSource = readFileSync(
  new URL("../src/admin/RequireAuth.tsx", import.meta.url),
  "utf8"
);

test("a failed session check does not turn an unknown session into a guest", () => {
  assert.match(
    authContextSource,
    /type AuthStatus = "loading" \| "authed" \| "guest" \| "unavailable"/
  );
  assert.match(
    authContextSource,
    /catch \{[\s\S]*?setStatus\(\(current\) =>[\s\S]*?current === "authed" \? current : "unavailable"/
  );
  assert.match(
    requireAuthSource,
    /status === "unavailable"[\s\S]*?<AuthUnavailable \/>/
  );
});
