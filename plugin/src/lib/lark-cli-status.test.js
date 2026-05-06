import assert from "node:assert/strict";
import test from "node:test";

import {
  buildLarkCliStatus,
  larkCliAuthLoginTool,
  larkCliConfigBindTool,
  larkCliConfigInitTool,
  larkCliDoctorStatusTool,
  larkCliExistsTool,
  larkCliIdentityStatusTool,
  larkCliLoginStatusTool,
  larkCliProfileUseTool,
  larkCliStatusTool,
  selectLarkProfile
} from "./lark-cli-status.js";

test("termind_lark_cli_status rejects old plan action", () => {
  const status = larkCliStatusTool({ action: "plan" });

  assert.equal(status.ready, false);
  assert.deepEqual(status.errors, ["unsupported action: plan"]);
  assert.equal(status.commands, undefined);
});

test("termind_lark_cli_status parses active valid profile and current user", () => {
  const status = buildLarkCliStatus({
    profileListOutput: JSON.stringify([
      { name: "old", active: false, tokenStatus: "no_token" },
      { name: "cli_a97d19e27838dcb6", active: true, tokenStatus: "valid" }
    ]),
    doctorExitCode: 0,
    doctorOutput: "ok",
    selfUserOutput: JSON.stringify({ data: { user: { open_id: "ou_test" } } })
  });

  assert.equal(status.installed, true);
  assert.equal(status.ready, true);
  assert.equal(status.profile, "cli_a97d19e27838dcb6");
  assert.equal(status.auth.userOpenID, "ou_test");
});

test("split lark-cli exists check plans one command and stops on missing cli", () => {
  const plan = larkCliExistsTool({ action: "plan" });
  assert.equal(plan.command, "lark-cli --version");
  assert.equal(plan.commands.length, 1);

  const parsed = larkCliExistsTool({ action: "parse", stderr: "zsh: command not found: lark-cli", exitCode: 127 });
  assert.equal(parsed.installed, false);
  assert.equal(parsed.stop, true);
  assert.equal(parsed.status.ready, false);
});

test("split lark-cli login check returns profile and next step", () => {
  const plan = larkCliLoginStatusTool({ action: "plan" });
  assert.equal(plan.command, "lark-cli profile list");
  assert.equal(plan.commands.length, 1);

  const parsed = larkCliLoginStatusTool({
    action: "parse",
    stdout: JSON.stringify([{ name: "cli_test", active: true, tokenStatus: "valid" }]),
    exitCode: 0
  });
  assert.equal(parsed.loggedIn, true);
  assert.equal(parsed.profile, "cli_test");
  assert.equal(parsed.next, "termind_lark_cli_doctor_status");
});

test("split lark-cli profile user metadata does not imply user sender identity", () => {
  const parsed = larkCliLoginStatusTool({
    action: "parse",
    stdout: JSON.stringify([{ name: "cli_test", active: true, tokenStatus: "valid", user: "Alice" }]),
    exitCode: 0
  });

  assert.equal(parsed.profiles[0].user, "Alice");
  assert.equal(parsed.profiles[0].identity, "bot");
});

test("split lark-cli explicit user identity is preserved", () => {
  const parsed = larkCliLoginStatusTool({
    action: "parse",
    stdout: JSON.stringify([{ name: "cli_test", active: true, tokenStatus: "valid", identity: "user" }]),
    exitCode: 0
  });

  assert.equal(parsed.profiles[0].identity, "user");
});

test("termind_lark_cli_profile_use plans and parses profile switch", () => {
  const plan = larkCliProfileUseTool({ action: "plan", profile: "cli test" });
  assert.equal(plan.command, "lark-cli profile use 'cli test'");
  assert.equal(plan.commands.length, 1);

  const parsed = larkCliProfileUseTool({
    action: "parse",
    profile: "cli test",
    stdout: "switched to cli test",
    exitCode: 0
  });
  assert.equal(parsed.ok, true);
  assert.equal(parsed.profile, "cli test");
  assert.deepEqual(parsed.errors, []);
});

test("termind_lark_cli_config_bind plans and parses existing OpenClaw bot bind", () => {
  const plan = larkCliConfigBindTool({ action: "plan", appId: "cli_a97d19e27838dcb6" });
  assert.equal(plan.command, "lark-cli config bind --source openclaw --app-id cli_a97d19e27838dcb6 --identity bot-only");
  assert.equal(plan.commands.length, 1);

  const parsed = larkCliConfigBindTool({
    action: "parse",
    appId: "cli_a97d19e27838dcb6",
    stdout: "bound cli_a97d19e27838dcb6",
    exitCode: 0
  });
  assert.equal(parsed.ok, true);
  assert.equal(parsed.appId, "cli_a97d19e27838dcb6");
  assert.equal(parsed.identity, "bot-only");
  assert.equal(parsed.profile, "cli_a97d19e27838dcb6");
  assert.deepEqual(parsed.errors, []);
});

test("termind_lark_cli_config_init builds manual OpenClaw-side bot init command", () => {
  const plan = larkCliConfigInitTool({ action: "plan", appId: "cli_a97d19e27838dcb6" });

  assert.equal(plan.ok, true);
  assert.equal(plan.manual, true);
  assert.equal(plan.requiresSecretInput, true);
  assert.equal(plan.command, "lark-cli config init --app-id cli_a97d19e27838dcb6 --brand feishu --app-secret-stdin");
  assert.deepEqual(plan.commands, []);
  assert.deepEqual(plan.errors, []);
});

test("termind_lark_cli_auth_login plans and parses OpenClaw-side device flow", () => {
  const startPlan = larkCliAuthLoginTool({ action: "plan", phase: "start" });
  assert.equal(startPlan.command, "lark-cli auth login --recommend --no-wait --json");

  const startParsed = larkCliAuthLoginTool({
    action: "parse",
    phase: "start",
    stdout: JSON.stringify({
      data: {
        device_code: "dev-test",
        user_code: "ABCD-EFGH",
        verification_uri_complete: "https://example.com/device?user_code=ABCD-EFGH",
        expires_in: 600,
        interval: 5
      }
    }),
    exitCode: 0
  });
  assert.equal(startParsed.ok, true);
  assert.equal(startParsed.deviceCode, "dev-test");
  assert.equal(startParsed.userCode, "ABCD-EFGH");
  assert.equal(startParsed.verificationURL, "https://example.com/device?user_code=ABCD-EFGH");
  assert.equal(startParsed.expiresIn, 600);

  const completePlan = larkCliAuthLoginTool({ action: "plan", phase: "complete", deviceCode: "dev-test" });
  assert.equal(completePlan.command, "lark-cli auth login --device-code dev-test --json");

  const completeParsed = larkCliAuthLoginTool({ action: "parse", phase: "complete", stdout: "{\"ok\":true}", exitCode: 0 });
  assert.equal(completeParsed.ok, true);
  assert.deepEqual(completeParsed.errors, []);
});

test("split lark-cli checks ignore caller profile and use OpenClaw active state", () => {
  const login = larkCliLoginStatusTool({
    action: "parse",
    profile: "cli_preferred",
    stdout: JSON.stringify([
      { name: "cli_active", active: true, tokenStatus: "valid" },
      { name: "cli_preferred", active: false, tokenStatus: "valid" }
    ]),
    exitCode: 0
  });
  assert.equal(login.loggedIn, true);
  assert.equal(login.profile, "cli_active");

  const identityPlan = larkCliIdentityStatusTool({ action: "plan", profile: "cli_preferred" });
  assert.equal(identityPlan.command, "lark-cli contact +get-user --as user --format json");

  const doctorPlan = larkCliDoctorStatusTool({ action: "plan", profile: "cli_preferred" });
  assert.equal(doctorPlan.command, "lark-cli doctor --offline");
});

test("split lark-cli identity check returns ready-shaped status", () => {
  const plan = larkCliIdentityStatusTool({ action: "plan" });
  assert.equal(plan.command, "lark-cli contact +get-user --as user --format json");
  assert.equal(plan.commands.length, 1);

  const parsed = larkCliIdentityStatusTool({
    action: "parse",
    profiles: [{ name: "cli_test", active: true, tokenStatus: "valid" }],
    stdout: JSON.stringify({ data: { user: { open_id: "ou_test" } } })
  });
  assert.equal(parsed.userOpenID, "ou_test");
  assert.equal(parsed.stop, false);
  assert.equal(parsed.status.ready, true);
  assert.equal(parsed.status.auth.userOpenID, "ou_test");
});

test("split lark-cli identity failure does not make bot profile unready", () => {
  const parsed = larkCliIdentityStatusTool({
    action: "parse",
    profiles: [{ name: "cli_bot", active: true, tokenStatus: "valid" }],
    stderr: "API call failed: need_user_authorization (user: ou_test)"
  });

  assert.equal(parsed.stop, false);
  assert.equal(parsed.status.ready, true);
  assert.ok(parsed.status.errors[0].includes("bot profile can still be ready"));
});

test("selectLarkProfile prefers active valid profile", () => {
  const got = selectLarkProfile([
    { name: "inactive-valid", active: false, tokenValid: true },
    { name: "active-valid", active: true, tokenValid: true },
    { name: "active-invalid", active: true, tokenValid: false }
  ]);

  assert.equal(got.name, "active-valid");
});
