#!/usr/bin/env node
'use strict';

const path = require('path');
const { spawnSync } = require('child_process');

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
  // Ctrl-C reaches the whole process group: the binary owns the graceful
  // shutdown, and the launcher must outlive it to report its exit status.
  const ignore = () => {};
  process.on('SIGINT', ignore);
  process.on('SIGTERM', ignore);
  const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });
  if (result.error) {
    console.error(`hookspot: ${result.error.message}`);
    process.exit(1);
  }
  if (result.signal) {
    process.removeListener('SIGINT', ignore);
    process.removeListener('SIGTERM', ignore);
    process.kill(process.pid, result.signal);
  }
  process.exit(result.status === null ? 1 : result.status);
}

module.exports = { resolveBinary };

if (require.main === module) {
  main();
}
