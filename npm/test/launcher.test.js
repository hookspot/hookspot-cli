'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const { test } = require('node:test');

const launcher = path.join(__dirname, '..', 'bin', 'hookspot.js');
const { resolveBinary } = require(launcher);

const root = path.join('pkg', 'root');

test('resolveBinary maps every supported platform and arch to a bundled binary', () => {
  const cases = [
    ['darwin', 'x64', path.join(root, 'binaries', 'darwin-amd64', 'hookspot')],
    ['darwin', 'arm64', path.join(root, 'binaries', 'darwin-arm64', 'hookspot')],
    ['linux', 'x64', path.join(root, 'binaries', 'linux-amd64', 'hookspot')],
    ['linux', 'arm64', path.join(root, 'binaries', 'linux-arm64', 'hookspot')],
    ['win32', 'x64', path.join(root, 'binaries', 'windows-amd64', 'hookspot.exe')],
    ['win32', 'arm64', path.join(root, 'binaries', 'windows-arm64', 'hookspot.exe')],
  ];
  for (const [platform, arch, expected] of cases) {
    assert.equal(resolveBinary(platform, arch, root), expected, `${platform}/${arch}`);
  }
});

test('resolveBinary returns null for unsupported platforms and archs', () => {
  assert.equal(resolveBinary('linux', 'ia32', root), null);
  assert.equal(resolveBinary('freebsd', 'x64', root), null);
});

test('launcher refuses an unsupported platform before spawning anything', (t) => {
  const dir = tempDir(t);
  const preload = path.join(dir, 'freebsd.js');
  fs.writeFileSync(preload, "Object.defineProperty(process, 'platform', { value: 'freebsd' });\n");
  const result = spawnSync(process.execPath, ['-r', preload, launcher], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.equal(result.stderr, `hookspot: unsupported platform freebsd/${process.arch}\n`);
});

// A stub shell script needs a POSIX shell, so the spawn tests skip on Windows.
const spawnTest = { skip: process.platform === 'win32' && 'stub binary requires a POSIX shell' };

function tempDir(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'hookspot-launcher-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// Copies the launcher into a temporary package root whose binaries/ tree holds
// the given shell script as the binary, or no binary at all.
function stagePackage(t, script) {
  const dir = tempDir(t);
  fs.mkdirSync(path.join(dir, 'bin'));
  fs.copyFileSync(launcher, path.join(dir, 'bin', 'hookspot.js'));
  if (script !== null) {
    const binary = resolveBinary(process.platform, process.arch, dir);
    fs.mkdirSync(path.dirname(binary), { recursive: true });
    fs.writeFileSync(binary, `#!/bin/sh\n${script}\n`, { mode: 0o755 });
  }
  return dir;
}

function runLauncher(dir, args) {
  return spawnSync(process.execPath, [path.join(dir, 'bin', 'hookspot.js'), ...args], { encoding: 'utf8' });
}

test('launcher forwards every argument and passes the exit status through', spawnTest, (t) => {
  const dir = stagePackage(t, 'printf \'%s\\n\' "$@"\nexit "$1"');
  const ok = runLauncher(dir, ['0']);
  assert.equal(ok.status, 0);
  assert.equal(ok.stdout, '0\n');
  const result = runLauncher(dir, ['3', 'b c', '--flag']);
  assert.equal(result.status, 3);
  assert.equal(result.stdout, '3\nb c\n--flag\n');
});

test('launcher exits 1 when the binary cannot be spawned', spawnTest, (t) => {
  const dir = stagePackage(t, null);
  const result = runLauncher(dir, ['0']);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /^hookspot: .*ENOENT/);
});

test('launcher dies by the same signal as the binary', spawnTest, (t) => {
  const dir = stagePackage(t, 'kill -TERM $$');
  const result = runLauncher(dir, []);
  assert.equal(result.signal, 'SIGTERM');
});

test('launcher ignores SIGINT and reports the status the binary chose', spawnTest, async (t) => {
  const dir = stagePackage(t, 'sleep 1\nexit 7');
  const child = spawn(process.execPath, [path.join(dir, 'bin', 'hookspot.js')], { stdio: 'ignore' });
  setTimeout(() => child.kill('SIGINT'), 300);
  const [status, signal] = await new Promise((resolve) => child.on('exit', (s, sig) => resolve([s, sig])));
  assert.equal(signal, null);
  assert.equal(status, 7);
});
