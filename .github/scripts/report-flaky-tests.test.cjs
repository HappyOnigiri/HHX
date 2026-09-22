'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const childProcess = require('node:child_process');
const reporter = require('./report-flaky-tests.cjs');

const REPO_ROOT = path.resolve(__dirname, '..', '..');
const PROFILE = 'race-coverage';
const JOB = 'test-race-coverage';
const SHA = '0123456789abcdef0123456789abcdef01234567';
const OWNER = 'octo-org';
const REPO = 'hhx';
const RUN_URL = `https://github.com/${OWNER}/${REPO}/actions/runs`;

function writeZip(t, files) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'hhx-ci-report-test-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const source = path.join(directory, 'source');
  for (const [name, body] of Object.entries(files)) {
    const target = path.join(source, name);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, typeof body === 'number' ? '' : body);
    if (typeof body === 'number') fs.truncateSync(target, body);
  }
  const zipPath = path.join(directory, 'artifact.zip');
  childProcess.execFileSync('zip', ['-qr', zipPath, '.'], { cwd: source });
  return zipPath;
}

function recovery(testPath = 'internal/example/flaky_test.go') {
  return {
    package: 'example.test',
    declaration: { path: testPath, function: 'TestFlaky', line: 12 },
    failed_tests: ['TestFlaky/sub test'],
    retry_index: 1,
  };
}

function manifest(runId, attempt, profile = PROFILE) {
  return {
    schema_version: 1,
    profile,
    recovered: true,
    run_id: String(runId),
    run_attempt: String(attempt),
    test_sha: SHA,
    initial: { command: ['go', 'test', '-json'], shuffle: '123' },
    retries: [{ command: ['go', 'test', '-run=^TestFlaky$'], shuffle: '123' }],
    recoveries: [recovery()],
  };
}

const source = {
  owner: OWNER,
  repo: REPO,
  runId: '10',
  attempt: '1',
  event: 'push',
  ref: 'main',
  testSha: SHA,
  runUrl: `${RUN_URL}/10`,
  commitUrl: `https://github.com/${OWNER}/${REPO}/commit/${SHA}`,
};

function sourceRun(id, overrides = {}) {
  return {
    name: 'CI', path: '.github/workflows/ci.yml', status: 'completed', run_attempt: 1,
    event: 'push', head_branch: 'main', head_sha: SHA, html_url: `${RUN_URL}/${id}`, ...overrides,
  };
}

function ciJob(overrides = {}) {
  return {
    name: JOB, run_attempt: 1, conclusion: 'success', html_url: `${RUN_URL}/10/job/1`,
    steps: [{ name: 'Upload CI test report', conclusion: 'success' }], ...overrides,
  };
}

// fakeGitHub は reporter が呼ぶ Actions と issue の API を、メモリ上の状態で置き換える。
// views を渡すと、issue の一覧 API はその中身を先頭から1回ずつ返す。反映の遅れた一覧を再現するのに使う。
function fakeGitHub({ run = sourceRun, jobs = [ciJob()], artifacts, issues = [], comments = new Map(), calls = [], views = [] } = {}) {
  let currentRun = '10';
  return { rest: {
    actions: {
      getWorkflowRun: async ({ run_id: id }) => { currentRun = String(id); return { data: run(id) }; },
      listJobsForWorkflowRun: async () => ({ data: { jobs } }),
      listWorkflowRunArtifacts: async () => ({ data: { artifacts: artifacts ?? [
        { id: 1, name: `ci-tests-${PROFILE}-${currentRun}-1`, expired: false, workflow_run: { id: Number(currentRun) } },
      ] } }),
    },
    issues: {
      listForRepo: async () => ({ data: views.length > 0 ? views.shift() : [...issues] }),
      listComments: async ({ issue_number: number }) => ({ data: comments.get(number) || [] }),
      create: async (request) => {
        const issue = { number: issues.length + 1, title: request.title, body: request.body, state: 'open' };
        issues.push(issue);
        calls.push(['create', issue.number]);
        return { data: issue };
      },
      createComment: async ({ issue_number: number, body }) => {
        const list = comments.get(number) || [];
        list.push({ body });
        comments.set(number, list);
        calls.push(['comment', number]);
        return { data: {} };
      },
      update: async ({ issue_number: number, state }) => {
        calls.push(['update', state]);
        const issue = issues.find((item) => item.number === number);
        if (issue) issue.state = state;
        return { data: {} };
      },
    },
  } };
}

function runOptions(github, runId = '10', extra = {}) {
  return {
    github, owner: OWNER, repo: REPO, sourceRunId: runId, sourceAttempt: '1', settleMs: 0,
    reports: [{ artifactName: `ci-tests-${PROFILE}-${runId}-1`, manifest: manifest(runId, '1') }],
    ...extra,
  };
}

test('marks issues with the hhx-flaky marker', () => {
  const group = reporter.aggregateManifests([{ artifactName: `ci-tests-${PROFILE}-10-1`, manifest: manifest('10', '1') }], source)[0];
  assert.match(group.marker, /^<!-- hhx-flaky: [0-9a-f]{64} -->$/);
  assert.equal(group.title, '[flaky] internal/example/flaky_test.go: TestFlaky');
  assert.ok(reporter.buildIssueBody(group, source).startsWith(group.marker));
});

test('creates once and comments on a later occurrence of the same issue', async () => {
  const calls = [];
  const github = fakeGitHub({ calls });
  const group1 = reporter.aggregateManifests([{ artifactName: 'report', manifest: manifest('10', '1') }], source)[0];
  assert.equal(await reporter.upsertGroup({ github, owner: OWNER, repo: REPO, group: group1, source, settleMs: 0 }), 'created');
  const source2 = { ...source, runId: '11', runUrl: `${RUN_URL}/11` };
  const group2 = reporter.aggregateManifests([{ artifactName: 'report', manifest: manifest('11', '1') }], source2)[0];
  assert.equal(await reporter.upsertGroup({ github, owner: OWNER, repo: REPO, group: group2, source: source2, settleMs: 0 }), 'commented');
  assert.deepEqual(calls, [['create', 1], ['comment', 1]]);
});

test('reopens a closed issue before commenting', async () => {
  const calls = [];
  const issues = [{ number: 3, title: '[flaky] internal/example/flaky_test.go: TestFlaky', body: '', state: 'closed' }];
  const github = fakeGitHub({ issues, calls });
  const group = reporter.aggregateManifests([{ artifactName: 'report', manifest: manifest('10', '1') }], source)[0];
  assert.equal(await reporter.upsertGroup({ github, owner: OWNER, repo: REPO, group, source, settleMs: 0 }), 'reopened-commented');
  assert.deepEqual(calls, [['update', 'open'], ['comment', 3]]);
});

test('uses an issue that the first listing missed instead of creating another', async () => {
  const calls = [];
  const group = reporter.aggregateManifests([{ artifactName: 'report', manifest: manifest('10', '1') }], source)[0];
  const issues = [{ number: 1, title: group.title, body: reporter.buildIssueBody(group, source), state: 'open' }];
  const github = fakeGitHub({ issues, calls, views: [[]] });
  assert.equal(await reporter.upsertGroup({ github, owner: OWNER, repo: REPO, group, source, settleMs: 0 }), 'already-recorded');
  assert.deepEqual(calls, []);
});

test('closes its own issue when another run created the same one first', async () => {
  const calls = [];
  const warnings = [];
  const group = reporter.aggregateManifests([{ artifactName: 'report', manifest: manifest('10', '1') }], source)[0];
  const issues = [{ number: 1, title: group.title, body: reporter.buildIssueBody(group, source), state: 'open' }];
  const github = fakeGitHub({ issues, calls, views: [[], []] });
  const summarySource = { ...source, summary: (message) => warnings.push(message) };
  assert.equal(await reporter.upsertGroup({ github, owner: OWNER, repo: REPO, group, source: summarySource, settleMs: 0 }), 'already-recorded');
  assert.deepEqual(calls, [['create', 2], ['comment', 2], ['update', 'closed']]);
  assert.equal(issues[1].state, 'closed');
  assert.deepEqual(warnings, [`closed duplicate issue #2 for ${group.title}; kept #1`]);
});

test('runs the report workflow with mocked Actions and issue APIs', async () => {
  const github = fakeGitHub({ jobs: [ciJob({ conclusion: 'failure' })] });
  const first = await reporter.run(runOptions(github, '10'));
  assert.deepEqual(first.results.map((item) => item.action), ['created']);
  assert.equal(first.groups[0].items[0].jobUrl, `${RUN_URL}/10/job/1`);
  const second = await reporter.run(runOptions(github, '11'));
  assert.deepEqual(second.results.map((item) => item.action), ['commented']);
});

test('records a run only once when it is reported again', async () => {
  const calls = [];
  const github = fakeGitHub({ calls });
  assert.deepEqual((await reporter.run(runOptions(github))).results.map((item) => item.action), ['created']);
  assert.deepEqual((await reporter.run(runOptions(github))).results.map((item) => item.action), ['already-recorded']);
  assert.deepEqual(calls, [['create', 1]]);
});

test('fails when a job that uploaded its report has no artifact', async () => {
  const warnings = [];
  const github = fakeGitHub({ artifacts: [] });
  await assert.rejects(reporter.run(runOptions(github, '10', {
    reports: [],
    core: { warning: (message) => warnings.push(message) },
  })), new RegExp(`missing report artifact for ${PROFILE}`));
  assert.deepEqual(warnings, [`missing report artifact for ${PROFILE}`]);
});

test('does not expect an artifact from a job that stopped before uploading', async () => {
  const github = fakeGitHub({ jobs: [ciJob({ conclusion: 'cancelled', steps: [] })], artifacts: [] });
  const result = await reporter.run(runOptions(github, '10', { reports: [] }));
  assert.equal(result.results.length, 0);
});

test('rejects a manifest that names another profile than its artifact', () => {
  assert.throws(() => reporter.aggregateManifests([
    { artifactName: `ci-tests-${PROFILE}-10-1`, manifest: manifest('10', '1', 'coverage') },
  ], source), /unsupported profile/);
});

test('rejects a manifest from another run', () => {
  assert.throws(() => reporter.aggregateManifests([
    { artifactName: `ci-tests-${PROFILE}-10-1`, manifest: manifest('11', '1') },
  ], source), /run_id does not match source run/);
});

test('rejects a workflow that is not in the contract table', async () => {
  const github = fakeGitHub({ run: (id) => sourceRun(id, { name: 'Flake Hunt', path: '.github/workflows/flake-hunt.yml' }) });
  await assert.rejects(reporter.run(runOptions(github)), /not a supported workflow/);
});

test('accepts a workflow path that carries a ref', async () => {
  const github = fakeGitHub({ run: (id) => sourceRun(id, { path: '.github/workflows/ci.yml@main' }) });
  assert.deepEqual((await reporter.run(runOptions(github))).results.map((item) => item.action), ['created']);
});

test('rejects a source run that has not completed', async () => {
  const github = fakeGitHub({ run: (id) => sourceRun(id, { status: 'in_progress' }) });
  await assert.rejects(reporter.run(runOptions(github)), /has not completed/);
});

test('caps how many issues one run opens', async () => {
  const value = manifest('10', '1');
  value.recoveries = [];
  for (let index = 0; index < 25; index += 1) {
    value.recoveries.push(recovery(`internal/example/flaky${String(index).padStart(2, '0')}_test.go`));
  }
  const github = fakeGitHub();
  const result = await reporter.run(runOptions(github, '10', {
    reports: [{ artifactName: `ci-tests-${PROFILE}-10-1`, manifest: value }],
  }));
  assert.equal(result.results.length, 20);
  assert.equal(result.skipped.length, 5);
});

test('does not count comments on existing issues toward the limit', async () => {
  const value = manifest('10', '1');
  value.recoveries = [];
  const issues = [];
  for (let index = 0; index < 21; index += 1) {
    const item = recovery(`internal/example/flaky${String(index).padStart(2, '0')}_test.go`);
    value.recoveries.push(item);
    if (index < 20) issues.push({ number: index + 1, title: reporter.issueTitle(item.declaration), body: '', state: 'open' });
  }
  const github = fakeGitHub({ issues });
  const result = await reporter.run(runOptions(github, '10', {
    reports: [{ artifactName: `ci-tests-${PROFILE}-10-1`, manifest: value }],
  }));
  assert.equal(result.results.filter((item) => item.action === 'commented').length, 20);
  assert.equal(result.results.filter((item) => item.action === 'created').length, 1);
  assert.equal(result.skipped.length, 0);
});

test('rejects a path escape in an artifact manifest', () => {
  const value = manifest('10', '1');
  value.recoveries[0].declaration.path = '../outside.go';
  assert.throws(() => reporter.validateManifest(value), /repository-relative path/);
});

test('keeps artifact content from breaking out of markdown', () => {
  const value = manifest('10', '1');
  value.recoveries[0].package = 'evil`![](https://attacker.example/pixel.png)';
  value.initial.status = '![](https://attacker.example/prose.png)';
  const body = reporter.buildIssueBody(reporter.aggregateManifests([{ artifactName: 'report', manifest: value }], source)[0], source);
  // code span内の画像記法は解釈されないので、spanが閉じないことだけを確かめる。
  assert.ok(!body.includes('evil`'), body);
  assert.match(body, /^- package: `evil｀!\[\]\(https:\/\/attacker\.example\/pixel\.png\)`$/mu);
  // 地の文では記法そのものを無効化する。
  assert.ok(body.includes('- result: initial \\!\\[\\](https://attacker.example/prose.png); retry unknown'), body);
});

test('keeps log excerpts on separate lines and shows angle brackets as written', () => {
  const value = manifest('10', '1');
  value.initial.log_excerpt = 'line1\nline2 <nil>\n<!-- hhx-flaky: fake -->';
  const body = reporter.buildIssueBody(reporter.aggregateManifests([{ artifactName: 'report', manifest: value }], source)[0], source);
  assert.ok(body.includes('```text\nline1\nline2 ＜nil＞\n＜!-- hhx-flaky: fake --＞\n```'), body);
  assert.ok(!body.includes('&lt;'), body);
});

test('reads manifest.json out of an artifact zip', (t) => {
  const zipPath = writeZip(t, { [`${PROFILE}/manifest.json`]: JSON.stringify(manifest('10', '1')), [`${PROFILE}/initial.log`]: 'log' });
  assert.equal(reporter.readZipManifest(zipPath, `ci-tests-${PROFILE}-10-1`).profile, PROFILE);
});

test('rejects an artifact that expands beyond the size limit before extracting it', (t) => {
  const zipPath = writeZip(t, { 'manifest.json': JSON.stringify(manifest('10', '1')), 'big.bin': 101 * 1024 * 1024 });
  assert.throws(() => reporter.readZipManifest(zipPath, `ci-tests-${PROFILE}-10-1`), /expands beyond the size limit/);
});

test('accepts a successful manifest without recoveries', () => {
  const value = manifest('10', '1');
  delete value.recoveries;
  value.recovered = false;
  assert.deepEqual(reporter.validateManifest(value).recoveries, []);
  assert.equal(reporter.aggregateManifests([{ artifactName: 'report', manifest: value }], source).length, 0);
});

// 以下はワークフローと Makefile と citest の間の名前の契約を確かめる。
// どれかが食い違うと、workflow_run が発火しない・artifact が見つからないなどの形で黙って起票が止まる。
function readRepoFile(name) {
  return fs.readFileSync(path.join(REPO_ROOT, name), 'utf8');
}

test('the report workflow listens to the CI workflow by its name', () => {
  const ci = readRepoFile(reporter.WORKFLOW_CONTRACT.path);
  assert.match(ci, new RegExp(`^name: ${reporter.WORKFLOW_CONTRACT.name}$`, 'm'));
  const report = readRepoFile('.github/workflows/report-flaky-tests.yml');
  const listened = /^\s+workflows: \[([^\]]*)\]$/m.exec(report);
  assert.ok(listened, 'report-flaky-tests.yml has no workflows: list');
  assert.deepEqual(listened[1].split(',').map((name) => name.trim()), [reporter.WORKFLOW_CONTRACT.name]);
});

test('the CI workflow uploads each profile under the names the reporter expects', () => {
  const ci = readRepoFile(reporter.WORKFLOW_CONTRACT.path);
  assert.match(ci, new RegExp(`- name: ${reporter.CI_UPLOAD_STEP}$`, 'm'));
  assert.ok(ci.includes('name: ci-tests-${{ matrix.citest-profile }}-${{ github.run_id }}-${{ github.run_attempt }}'));
  assert.ok(ci.includes('path: artifacts/ci-tests/${{ matrix.citest-profile }}'));
  assert.match(ci, /^\s+name: \$\{\{ matrix\.target \}\}$/m);
  const makefile = readRepoFile('Makefile');
  const citest = readRepoFile('tools/citest/main.go');
  for (const { profile, job } of reporter.PROFILE_CONTRACTS) {
    assert.match(ci, new RegExp(`- target: ${job}\\n(?:\\s+[a-z-]+: .*\\n)*?\\s+citest-profile: ${profile}$`, 'm'));
    assert.ok(makefile.includes(`-profile ${profile} -report-dir "$(CI_TEST_ARTIFACT_DIR)/${profile}"`), `Makefile does not run profile ${profile}`);
    assert.ok(citest.includes(`"${profile}": true`), `citest does not accept profile ${profile}`);
  }
});
