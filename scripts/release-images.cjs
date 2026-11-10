// Publish images during prepare, before semantic-release creates its Git tag.
const { execFileSync } = require('node:child_process');

exports.publishImages = function (version, revision, run = execFileSync) {
  if (!/^\d+\.\d+\.\d+$/.test(version) || !/^[a-f0-9]{40}$/.test(revision)) {
    throw new Error('Expected a stable release version and full Git commit SHA');
  }
  const images = [
    ['runtime', 'ghcr.io/l13t/vartovyi'],
    ['media', 'ghcr.io/l13t/vartovyi-media'],
  ];
  for (const [target, image] of images) {
    run('docker', ['buildx', 'build', '--platform', 'linux/amd64,linux/arm64',
      '--target', target, '--build-arg', `VERSION=${version}`, '--tag', `${image}:${version}`,
      '--label', 'org.opencontainers.image.source=https://github.com/l13t/vartovyi',
      '--label', `org.opencontainers.image.version=${version}`,
      '--label', `org.opencontainers.image.revision=${revision}`,
      '--push', '.'], { stdio: 'inherit' });
  }
  // Promote latest only after both versioned images have been published.
  for (const [, image] of images) {
    run('docker', ['buildx', 'imagetools', 'create', '--tag', `${image}:latest`,
      `${image}:${version}`], { stdio: 'inherit' });
  }
};

exports.prepare = async function (_config, { nextRelease }) {
  exports.publishImages(nextRelease.version, nextRelease.gitHead);
};
