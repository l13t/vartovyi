const { test } = require('node:test');
const assert = require('node:assert/strict');
const { publishImages } = require('./release-images.cjs');

test('publishes both multi-platform targets before promoting latest', () => {
  const calls = [];
  publishImages('1.2.3', 'a'.repeat(40), (cmd, args) => calls.push([cmd, args]));
  assert.equal(calls.length, 4);
  for (const [index, target] of ['runtime', 'media'].entries()) {
    const [cmd, args] = calls[index];
    assert.equal(cmd, 'docker');
    assert.equal(args[args.indexOf('--platform') + 1], 'linux/amd64,linux/arm64');
    assert.equal(args[args.indexOf('--target') + 1], target);
    assert.equal(args[args.indexOf('--build-arg') + 1], 'VERSION=1.2.3');
    assert.ok(args.includes('--push'));
    assert.ok(args.includes(`ghcr.io/l13t/vartovyi${index ? '-media' : ''}:1.2.3`));
  }
  assert.deepEqual(calls[2][1], ['buildx', 'imagetools', 'create', '--tag',
    'ghcr.io/l13t/vartovyi:latest', 'ghcr.io/l13t/vartovyi:1.2.3']);
  assert.deepEqual(calls[3][1], ['buildx', 'imagetools', 'create', '--tag',
    'ghcr.io/l13t/vartovyi-media:latest', 'ghcr.io/l13t/vartovyi-media:1.2.3']);
});

test('a failed build aborts release before latest promotion', () => {
  let calls = 0;
  assert.throws(() => publishImages('1.2.3', 'a'.repeat(40), () => {
    if (++calls === 2) throw new Error('build failed');
  }), /build failed/);
  assert.equal(calls, 2);
});

test('invalid release metadata cannot publish images', () => {
  for (const [version, sha] of [['1.2.3-rc.1', 'a'.repeat(40)], ['1.2.3', 'bad']]) {
    assert.throws(() => publishImages(version, sha, () => assert.fail('must not run')));
  }
});

test('configured commit rules select patch, minor, major or no release', async () => {
  const { analyzeCommits } = await import('@semantic-release/commit-analyzer');
  const config = require('../.releaserc.json');
  assert.deepEqual(config.branches, ['master']);
  const options = config.plugins.find(p => p[0] === '@semantic-release/commit-analyzer')[1];
  for (const [message, expected] of [
    ['fix: recover recording after reconnect', 'patch'],
    ['feat: add camera presets', 'minor'],
    ['feat!: change camera contract', 'major'],
    ['fix: change contract\n\nBREAKING CHANGE: remove legacy field', 'major'],
    ['docs: describe setup', null],
    ['chore: update tooling', null],
  ]) {
    assert.equal(await analyzeCommits(options, {
      cwd: process.cwd(), commits: [{ hash: 'a'.repeat(40), message }],
      logger: { log() {} },
    }), expected, message);
  }
});
