#!/usr/bin/env node
'use strict';

const path = require('path');
const { spawn } = require('child_process');

const platforms = { darwin: 'darwin', linux: 'linux', win32: 'windows' };
const arches = { x64: 'amd64', arm64: 'arm64' };

// Maps a Node platform/arch pair to the bundled GoReleaser binary, or null
// when no binary is shipped for it.
function resolveBinary(platform, arch, root) {
  const os = platforms[platform];
  const goArch = arches[arch];
  if (!os || !goArch) {
    return null;
  }
  const ext = os === 'windows' ? '.exe' : '';
  return path.join(root, 'binaries', `${os}-${goArch}`, `hookspot${ext}`);
}

function main() {
  const binary = resolveBinary(process.platform, process.arch, path.join(__dirname, '..'));
  if (!binary) {
    console.error(`hookspot: unsupported platform ${process.platform}/${process.arch}`);
    process.exit(1);
  }
  const child = spawn(binary, process.argv.slice(2), { stdio: 'inherit' });
  // Ctrl-C reaches the whole process group, so the binary already sees it and
  // forwarding would double-deliver, triggering its forced exit. SIGTERM from
  // a supervisor targets the launcher alone, so it is passed on.
  process.on('SIGINT', () => {});
  process.on('SIGTERM', () => child.kill('SIGTERM'));
  child.on('error', (error) => {
    console.error(`hookspot: ${error.message}`);
    process.exit(1);
  });
  child.on('exit', (status, signal) => {
    if (signal) {
      process.removeAllListeners(signal);
      process.kill(process.pid, signal);
    }
    process.exit(status === null ? 1 : status);
  });
}

module.exports = { resolveBinary };

if (require.main === module) {
  main();
}
