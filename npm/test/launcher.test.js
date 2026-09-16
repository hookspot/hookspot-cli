'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
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

// A stub shell script needs a POSIX shell, so the spawn tests skip on Windows.
const spawnTest = { skip: process.platform === 'win32' && 'stub binary requires a POSIX shell' };

// Copies the launcher into a temporary package root whose binaries/ tree holds a
// stub that exits with the status given as its first argument.
function stagePackage(withBinary) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'hookspot-launcher-'));
  fs.mkdirSync(path.join(dir, 'bin'));
  fs.copyFileSync(launcher, path.join(dir, 'bin', 'hookspot.js'));
  if (withBinary) {
    const binary = resolveBinary(process.platform, process.arch, dir);
    fs.mkdirSync(path.dirname(binary), { recursive: true });
    fs.writeFileSync(binary, '#!/bin/sh\nexit "$1"\n', { mode: 0o755 });
  }
  return dir;
}

function runLauncher(dir, args) {
  return spawnSync(process.execPath, [path.join(dir, 'bin', 'hookspot.js'), ...args], { encoding: 'utf8' });
}

test('launcher passes the binary exit status through', spawnTest, (t) => {
  const dir = stagePackage(true);
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  assert.equal(runLauncher(dir, ['0']).status, 0);
  assert.equal(runLauncher(dir, ['3']).status, 3);
});

test('launcher exits 1 when the binary cannot be spawned', spawnTest, (t) => {
  const dir = stagePackage(false);
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const result = runLauncher(dir, ['0']);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /^hookspot: .*ENOENT/);
});
