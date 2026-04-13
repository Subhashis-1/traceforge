const fs = require("fs");
const os = require("os");
const path = require("path");
const { execFileSync } = require("child_process");
const { createRequire } = require("module");

function nodeModulesBin(tmpDir, command) {
  const ext = process.platform === "win32" ? ".cmd" : "";
  return path.join(tmpDir, "node_modules", ".bin", command + ext);
}

function runCommand(command, args, options = {}) {
  if (process.platform === "win32") {
    return execFileSync("cmd.exe", ["/c", command, ...args], options);
  }
  return execFileSync(command, args, options);
}

async function main() {
  const ingesterBaseURL = process.argv[2];
  if (!ingesterBaseURL) {
    throw new Error(
      "usage: node scripts/session_replay_test.js <ingester-base-url>",
    );
  }

  const repoRoot = path.resolve(__dirname, "..");
  const sdkDir = path.join(repoRoot, "pkg", "sdk-js");
  const tmpDir = fs.mkdtempSync(
    path.join(os.tmpdir(), "traceforge-session-replay-"),
  );
  process.env.PLAYWRIGHT_BROWSERS_PATH = "0";

  try {
    const packOutput = runCommand("npm", ["pack"], {
      cwd: sdkDir,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();

    const tarballName = packOutput.split(/\r?\n/).filter(Boolean).pop();
    if (!tarballName) {
      throw new Error("npm pack did not produce a tarball name");
    }

    const tarballPath = path.join(sdkDir, tarballName);
    runCommand("npm", ["init", "-y"], {
      cwd: tmpDir,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    });

    runCommand("npm", ["install", tarballPath, "playwright"], {
      cwd: tmpDir,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        PLAYWRIGHT_BROWSERS_PATH: "0",
      },
    });

    const playwrightCLI = nodeModulesBin(tmpDir, "playwright");
    runCommand(playwrightCLI, ["install", "chromium"], {
      cwd: tmpDir,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      env: {
        ...process.env,
        PLAYWRIGHT_BROWSERS_PATH: "0",
      },
    });

    const installedSDK = path.join(
      tmpDir,
      "node_modules",
      "@traceforge",
      "sdk-js",
      "dist",
      "traceforge-sdk.js",
    );
    if (!fs.existsSync(installedSDK)) {
      throw new Error(`installed SDK bundle not found at ${installedSDK}`);
    }

    const requireFromTmp = createRequire(path.join(tmpDir, "package.json"));
    const { chromium } = requireFromTmp("playwright");

    const browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();

    await page.goto(ingesterBaseURL, { waitUntil: "networkidle" });
    await page.waitForFunction(
      () => typeof window.TraceForgeSDK !== "undefined",
    );
    await page.evaluate((endpoint) => {
      window.TraceForgeSDK.init({ endpoint: `${endpoint}/v1/sessions` });
    }, ingesterBaseURL);

    await page.click("#btn");

    await page.evaluate(async (endpoint) => {
      try {
        await fetch(`${endpoint}/v1/traces`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "X-OTEL-TRACE-ID": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
          },
          body: "{}",
        });
      } catch (_) {}
    }, ingesterBaseURL);

    await page.click("#btn");
    await page.waitForTimeout(1000);

    const cookies = await page.context().cookies(ingesterBaseURL);
    const sessionCookie = cookies.find(
      (cookie) => cookie.name === "tf_session",
    );
    if (!sessionCookie || !sessionCookie.value) {
      throw new Error("TraceForge SDK did not create tf_session cookie");
    }

    await browser.close();
    process.stdout.write(JSON.stringify({ sessionId: sessionCookie.value }));
  } finally {
    try {
      for (const entry of fs.readdirSync(sdkDir)) {
        if (entry.startsWith("traceforge-sdk-js-") && entry.endsWith(".tgz")) {
          fs.rmSync(path.join(sdkDir, entry), { force: true });
        }
      }
    } catch (_) {}

    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch (_) {}
  }
}

main().catch((err) => {
  process.stderr.write(`${err.stack || err.message}\n`);
  process.exit(1);
});
