'use strict';

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');
const childProcess = require('node:child_process');

// citest の報告を上げる CI のジョブ。profile は citest の -profile と ci.yml の matrix の citest-profile、
// job は ci.yml のジョブの表示名（matrix の target）に合わせる。
const PROFILE_CONTRACTS = Object.freeze([
  Object.freeze({ profile: 'race-coverage', job: 'test-race-coverage' }),
]);
const PROFILES = new Set(PROFILE_CONTRACTS.map(({ profile }) => profile));
const PROFILE_PATTERN = PROFILE_CONTRACTS
  .map(({ profile }) => profile)
  .sort((a, b) => b.length - a.length)
  .map((profile) => profile.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  .join('|');
const ARTIFACT_PROFILE = new RegExp(`^ci-tests-(${PROFILE_PATTERN})-`);
const SHA = /^[0-9a-f]{40}$/i;
const MAX_TEXT = 12000;

// 起票の対象にする workflow。表示名とpathの両方が一致したものだけを受け付ける。
// workflows: に並ぶ名前と同じく workflow の name: に依存するので、名前を変えるとここも直す必要がある。
const WORKFLOW_CONTRACT = Object.freeze({ name: 'CI', path: '.github/workflows/ci.yml' });

const CI_UPLOAD_STEP = 'Upload CI test report';
// 1つのrunで新しく開く issue の上限。既存の issue へのコメントは数えない。超えた分は step summary にだけ残す。
const MAX_ISSUES_PER_RUN = 20;
// issue の一覧 API は作成直後の issue を数秒遅れて返す。起票の前後でこれだけ待って照会し直し、
// 別の run が直前に作った同名の issue と重ならないようにする。
const SETTLE_MS = 3000;

// workflow run の path は `.github/workflows/ci.yml@main` のように ref を付けて返ることがある。
function workflowPath(value) {
  return String(value).split('@')[0];
}

function profileFromArtifactName(name) {
  return ARTIFACT_PROFILE.exec(name || '')?.[1] || '';
}

function artifactNamePattern(runId, attempt) {
  return new RegExp(`^ci-tests-(${PROFILE_PATTERN})-${runId}-${attempt}$`);
}

function fail(message) {
  throw new Error(`invalid CI test report: ${message}`);
}

function text(value, name, limit = MAX_TEXT) {
  if (typeof value !== 'string' || value.length > limit || /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/u.test(value)) {
    fail(`${name} is not a bounded text value`);
  }
  return value;
}

function relativePath(value, name) {
  text(value, name, 1000);
  const normalized = value.replaceAll('\\', '/');
  if (!normalized || normalized.startsWith('/') || normalized.includes('../') || normalized === '..' || normalized.includes('\u0000')) {
    fail(`${name} is not a repository-relative path`);
  }
  return normalized;
}

function validateManifest(value, artifactName = 'artifact') {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${artifactName} is not an object`);
  if (value.schema_version !== 1) fail(`${artifactName} has unsupported schema_version`);
  if (!PROFILES.has(value.profile)) fail(`${artifactName} has unsupported profile`);
  if (value.test_sha && !SHA.test(value.test_sha)) fail(`${artifactName} has invalid test_sha`);
  if (value.api_head_sha && !SHA.test(value.api_head_sha)) fail(`${artifactName} has invalid api_head_sha`);
  if (value.run_id && !/^\d+$/.test(String(value.run_id))) fail(`${artifactName} has invalid run_id`);
  if (value.run_attempt && !/^\d+$/.test(String(value.run_attempt))) fail(`${artifactName} has invalid run_attempt`);
  if (value.recoveries === undefined) value.recoveries = [];
  if (!Array.isArray(value.recoveries)) fail(`${artifactName} recoveries is not an array`);
  if (value.recoveries.length > 1000) fail(`${artifactName} has too many recoveries`);
  for (const [index, item] of value.recoveries.entries()) {
    if (!item || typeof item !== 'object') fail(`${artifactName} recovery ${index} is not an object`);
    const declaration = item.declaration;
    if (!declaration || typeof declaration !== 'object') fail(`${artifactName} recovery ${index} has no declaration`);
    relativePath(declaration.path, `${artifactName} recovery ${index} path`);
    const fn = text(declaration.function, `${artifactName} recovery ${index} function`, 500);
    if (!/^(Test|Benchmark|Fuzz|Example)[A-Za-z0-9_]+$/.test(fn)) fail(`${artifactName} recovery ${index} has invalid function`);
    text(item.package, `${artifactName} recovery ${index} package`, 500);
    if (!Array.isArray(item.failed_tests) || item.failed_tests.length === 0 || item.failed_tests.length > 100) {
      fail(`${artifactName} recovery ${index} has invalid failed_tests`);
    }
    for (const testName of item.failed_tests) text(testName, `${artifactName} recovery ${index} failed test`, 1000);
  }
  if (value.recovered !== true && value.recoveries.length !== 0) fail(`${artifactName} marks recoveries without recovered=true`);
  return value;
}

function marker({ owner, repo, runId, attempt, declaration }) {
  const raw = [owner, repo, String(runId), String(attempt), declaration.path, declaration.function].join('\n');
  return `<!-- hhx-flaky: ${crypto.createHash('sha256').update(raw).digest('hex')} -->`;
}

// アーティファクト由来の文字列をcode spanとfenced blockへ埋められる形へ均す。
// code span内はバックスラッシュでエスケープが効かず、\` ではspanが閉じてしまうため、
// バックティックは全角へ置き換える。code span と fenced block の中では実体参照が解釈されないので、
// marker の偽装を防ぐ < と > も実体参照ではなく全角へ置き換える。
function neutralize(value) {
  return value.replaceAll('`', '｀').replaceAll('@', '＠').replaceAll('<', '＜').replaceAll('>', '＞');
}

function sanitize(value, limit = 4000) {
  return neutralize(String(value ?? '').replace(/[\u0000-\u001f]/gu, ' ')).slice(0, limit);
}

// fenced block に埋めるログの抜粋に使う。改行だけは残し、複数行のまま読めるようにする。
function sanitizeBlock(value, limit = 4000) {
  return neutralize(String(value ?? '').replace(/[\u0000-\u0009\u000b-\u001f]/gu, ' ')).slice(0, limit);
}

// 地の文へ埋める値に使う。リンク・画像の記法はここでだけ解釈され、
// GitHubのプロキシ越しに外部を取得させるので無効化する。
// code spanやfenced blockではバックスラッシュがそのまま見えるため、同じ処理はしない。
function sanitizeText(value, limit = 4000) {
  return sanitize(value, limit).replace(/[[\]!]/gu, '\\$&');
}

function issueTitle(declaration) {
  return `[flaky] ${declaration.path}: ${declaration.function}`;
}

function excerpt(manifest, recovery, jobUrl = '') {
  const failed = recovery.failed_tests.join(', ');
  const initial = manifest.initial || {};
  const retry = (manifest.retries || [])[recovery.retry_index - 1] || {};
  return [
    `- profile: \`${manifest.profile}\``,
    jobUrl ? `- job: ${jobUrl}` : '- job: not recorded',
    `- package: \`${sanitize(recovery.package, 500)}\``,
    `- failed tests: \`${sanitize(failed, 1000)}\``,
    `- initial command: \`${sanitize((initial.command || []).join(' '), 2000)}\``,
    `- retry command: \`${sanitize((retry.command || []).join(' '), 2000)}\``,
    `- duration: initial ${sanitizeText(initial.duration_ms ?? 'unknown', 100)} ms; retry ${sanitizeText(retry.duration_ms ?? 'unknown', 100)} ms`,
    `- result: initial ${sanitizeText(initial.status || initial.exit || 'unknown', 100)}; retry ${sanitizeText(retry.status || retry.exit || 'unknown', 100)}`,
    `- shuffle seed: \`${sanitize(retry.shuffle || initial.shuffle || 'not recorded', 200)}\``,
    '',
    'Initial failure excerpt:',
    '```text',
    sanitizeBlock(initial.log_excerpt || 'not recorded', 4000),
    '```',
    '',
    'Retry output excerpt:',
    '```text',
    sanitizeBlock(retry.log_excerpt || 'not recorded', 4000),
    '```',
    `- evidence: artifact \`${sanitize(manifest.artifact_name || 'ci-test-report', 300)}\``,
    '',
    'The test passed on the one additional run. This records an observation, not a diagnosis of the test or product.',
  ].join('\n');
}

function buildIssueBody(group, source) {
  const first = group.items[0];
  const declaration = first.recovery.declaration;
  const lines = [
    group.marker,
    `## ${sanitizeText(issueTitle(declaration), 1000)}`,
    '',
    `Source workflow: [CI run ${source.runId}](${source.runUrl}) (attempt ${source.attempt})`,
    `Event: \`${sanitize(source.event, 200)}\`; ref: \`${sanitize(source.ref, 500)}\``,
    `API head SHA: [${source.apiHeadSha || 'unknown'}](${source.commitUrl || '#'})`,
    source.prUrl ? `PR: ${source.prUrl}` : 'PR: none',
    '',
    'The following CI test failure recovered on exactly one additional run:',
  ];
  for (const item of group.items) {
    lines.push('', `### ${sanitize(item.manifest.profile, 100)}`, `Test commit: \`${sanitize(item.manifest.test_sha || 'unknown', 100)}\``, excerpt(item.manifest, item.recovery, item.jobUrl));
  }
  return lines.join('\n').slice(0, 60000);
}

function buildIssueComment(group, source) {
  return [group.marker, `Reproduction observed in [CI run ${source.runId}](${source.runUrl}) (attempt ${source.attempt}).`, '', ...group.items.map((item) => `### ${sanitize(item.manifest.profile, 100)}\n${excerpt(item.manifest, item.recovery, item.jobUrl)}`)].join('\n').slice(0, 60000);
}

function aggregateManifests(manifests, source) {
  const groups = new Map();
  for (const item of manifests) {
    const manifest = validateManifest(item.manifest, item.artifactName);
    const artifactProfile = profileFromArtifactName(item.artifactName);
    if (artifactProfile && manifest.profile !== artifactProfile) fail(`${item.artifactName} profile does not match its artifact name`);
    if (manifest.run_id && String(manifest.run_id) !== String(source.runId)) fail(`${item.artifactName} run_id does not match source run`);
    if (manifest.run_attempt && String(manifest.run_attempt) !== String(source.attempt)) fail(`${item.artifactName} run_attempt does not match source attempt`);
    manifest.artifact_name = item.artifactName;
    for (const recovery of manifest.recoveries) {
      const key = `${recovery.declaration.path}\n${recovery.declaration.function}`;
      let group = groups.get(key);
      if (!group) {
        group = { key, marker: marker({ ...source, declaration: recovery.declaration }), title: issueTitle(recovery.declaration), items: [] };
        groups.set(key, group);
      }
      group.items.push({ manifest, recovery, jobUrl: item.jobUrl || '' });
    }
  }
  return [...groups.values()].sort((a, b) => a.title.localeCompare(b.title));
}

async function pages(fetchPage) {
  const all = [];
  for (let page = 1; page <= 100; page += 1) {
    const result = await fetchPage(page);
    const values = result.data?.items ?? result.data?.artifacts ?? result.data?.comments ?? result.data?.jobs ?? result.data ?? [];
    all.push(...values);
    if (values.length < 100) break;
  }
  return all;
}

async function listIssues(github, owner, repo) {
  return pages((page) => github.rest.issues.listForRepo({ owner, repo, state: 'all', per_page: 100, page })).then((issues) => issues.filter((item) => !item.pull_request));
}

async function listComments(github, owner, repo, issueNumber) {
  return pages((page) => github.rest.issues.listComments({ owner, repo, issue_number: issueNumber, per_page: 100, page }));
}

// 作成日の新しい順に1ページだけ読み、同名の issue を番号順に返す。
// 直前に作られた issue を探すための照会なので、古い issue は listIssues に任せる。
async function recentIssuesWithTitle(github, owner, repo, title) {
  const result = await github.rest.issues.listForRepo({ owner, repo, state: 'all', sort: 'created', direction: 'desc', per_page: 100, page: 1 });
  return (result.data || []).filter((item) => !item.pull_request && item.title === title).sort((a, b) => a.number - b.number);
}

function wait(ms) {
  return ms > 0 ? new Promise((resolve) => { setTimeout(resolve, ms); }) : Promise.resolve();
}

async function recordOnIssue({ github, owner, repo, group, source, issue }) {
  const comments = await listComments(github, owner, repo, issue.number);
  const found = [issue.body || '', ...comments.map((item) => item.body || '')].some((body) => body.includes(group.marker));
  if (found) return 'already-recorded';
  const wasClosed = issue.state === 'closed';
  if (wasClosed) await github.rest.issues.update({ owner, repo, issue_number: issue.number, state: 'open' });
  await github.rest.issues.createComment({ owner, repo, issue_number: issue.number, body: buildIssueComment(group, source) });
  return wasClosed ? 'reopened-commented' : 'commented';
}

// issues を渡すと全ページ取得を1回に巻き上げられる。作成した issue は同じ配列へ積み、
// 同じrunの後続グループから見えるようにする。
// canCreate が false のときは新しい issue を作らず 'over-limit' を返す。
async function upsertGroup({ github, owner, repo, group, source, issues, settleMs = SETTLE_MS, canCreate = true }) {
  const known = issues || await listIssues(github, owner, repo);
  const matching = known.filter((item) => item.title === group.title).sort((a, b) => a.number - b.number);
  // concurrencyのqueueが効かず複数のrunが同時に走ると、同じタイトルのissueが並び得る。
  // 先頭だけを使い、重複は警告として残す。
  if (matching.length > 1 && source.summary) source.summary(`duplicate issue titles for ${group.title}: ${matching.slice(1).map((item) => item.number).join(', ')}`);
  let issue = matching[0];
  if (!issue) {
    // 一覧の反映が遅れて、直前の run が作った issue が見えていないことがある。
    await wait(settleMs);
    issue = (await recentIssuesWithTitle(github, owner, repo, group.title))[0];
  }
  if (issue) return recordOnIssue({ github, owner, repo, group, source, issue });
  if (!canCreate) return 'over-limit';
  const body = buildIssueBody(group, source);
  const created = (await github.rest.issues.create({ owner, repo, title: group.title, body }))?.data;
  const createdNumber = created?.number ?? Number.MAX_SAFE_INTEGER;
  // 同時に走った run も同じ issue を作り得る。番号の最も小さい issue を正本にし、それ以外は閉じる。
  await wait(settleMs);
  const canonical = (await recentIssuesWithTitle(github, owner, repo, group.title))[0];
  if (canonical && created?.number && canonical.number < created.number) {
    await github.rest.issues.createComment({ owner, repo, issue_number: created.number, body: `Duplicate of #${canonical.number}.` });
    await github.rest.issues.update({ owner, repo, issue_number: created.number, state: 'closed', state_reason: 'not_planned' });
    if (source.summary) source.summary(`closed duplicate issue #${created.number} for ${group.title}; kept #${canonical.number}`);
    if (issues) issues.push(canonical);
    return recordOnIssue({ github, owner, repo, group, source, issue: canonical });
  }
  if (issues) issues.push({ number: createdNumber, title: group.title, body, state: 'open' });
  return 'created';
}

function zipEntries(zipPath) {
  const names = childProcess.execFileSync('unzip', ['-Z1', zipPath], { encoding: 'utf8' }).split('\n').filter(Boolean);
  if (names.length > 10000) fail('artifact has too many entries');
  for (const name of names) {
    const normalized = name.replaceAll('\\', '/');
    if (normalized.startsWith('/') || normalized.includes('../') || normalized === '..' || normalized.includes('\u0000')) fail('artifact contains unsafe archive entry');
  }
  return names;
}

// アーカイブが申告する非圧縮の合計サイズを、展開の前に読む。
function zipUncompressedSize(zipPath, artifactName) {
  const summary = childProcess.execFileSync('unzip', ['-Z', '-t', zipPath], { encoding: 'utf8' });
  const match = /(\d+)\s+bytes uncompressed/.exec(summary);
  if (!match) fail(`${artifactName} has no readable archive summary`);
  return Number(match[1]);
}

// アーティファクトはfork PRからも届くため、展開の前に上限を判定し、manifest.json以外は取り出さない。
// unzip -pはディスクへ書かず、maxBufferが実際の展開量も頭打ちにするので、
// 中央ディレクトリが小さいサイズを申告する高圧縮率のアーカイブでも被害が出ない。
function extractManifest(zipPath, artifactName) {
  const entries = zipEntries(zipPath);
  const manifestName = entries.find((name) => name === 'manifest.json' || name.endsWith('/manifest.json'));
  if (!manifestName) fail(`${artifactName} has no manifest.json`);
  if (/[*?[\\]/.test(manifestName)) fail(`${artifactName} has an unsupported manifest.json entry name`);
  if (zipUncompressedSize(zipPath, artifactName) > 100 * 1024 * 1024) fail(`${artifactName} expands beyond the size limit`);
  let data;
  try {
    data = childProcess.execFileSync('unzip', ['-p', zipPath, manifestName], { encoding: 'utf8', maxBuffer: 20 * 1024 * 1024 });
  } catch {
    fail(`${artifactName} manifest.json could not be extracted within the size limit`);
  }
  return JSON.parse(data);
}

function readZipManifest(zipPath, artifactName) {
  return validateManifest(extractManifest(zipPath, artifactName), artifactName);
}

function downloadArtifactZip(github, owner, repo, artifact, read) {
  return (async () => {
    const response = await github.rest.actions.downloadArtifact({ owner, repo, artifact_id: artifact.id, archive_format: 'zip' });
    const data = Buffer.isBuffer(response.data) ? response.data : Buffer.from(response.data);
    if (data.length > 50 * 1024 * 1024) fail(`${artifact.name} is too large`);
    const file = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'hhx-ci-artifact-')), `${artifact.id}.zip`);
    fs.writeFileSync(file, data, { mode: 0o600 });
    try {
      return read(file);
    } finally {
      fs.rmSync(path.dirname(file), { recursive: true, force: true });
    }
  })();
}

async function collectFromArtifacts({ github, owner, repo, artifacts }) {
  const result = [];
  for (const artifact of artifacts) {
    const manifest = await downloadArtifactZip(github, owner, repo, artifact, (file) => readZipManifest(file, artifact.name));
    const artifactProfile = profileFromArtifactName(artifact.name);
    if (!artifactProfile || manifest.profile !== artifactProfile) fail(`${artifact.name} profile does not match its artifact name`);
    result.push({ artifactName: artifact.name, manifest });
  }
  return result;
}

// collectCI は通常CIのrunから、profileごとのmanifestを集める。
// 期待するartifactはPROFILE_CONTRACTSの静的な表から決まる。
async function collectCI({ github, owner, repo, runId, attempt, source, options }) {
  const jobs = await pages((page) => github.rest.actions.listJobsForWorkflowRun({ owner, repo, run_id: Number(runId), filter: 'all', per_page: 100, page }));
  const jobProfiles = new Map(PROFILE_CONTRACTS.map(({ job, profile }) => [job, profile]));
  const expected = new Set();
  const jobUrls = new Map();
  for (const job of jobs) {
    if (String(job.run_attempt || attempt) !== attempt) continue;
    const profile = jobProfiles.get(job.name);
    if (!profile) continue;
    const uploaded = (job.steps || []).some((step) => step.name === CI_UPLOAD_STEP && step.conclusion === 'success');
    if (job.conclusion === 'success' || uploaded) expected.add(profile);
    if (job.html_url) jobUrls.set(profile, job.html_url);
  }
  const artifacts = await pages((page) => github.rest.actions.listWorkflowRunArtifacts({ owner, repo, run_id: Number(runId), per_page: 100, page }));
  const usable = artifacts.filter((artifact) => !artifact.expired && artifact.workflow_run?.id === Number(runId) && artifactNamePattern(runId, attempt).test(artifact.name));
  // 取りこぼした成果物は最後に失敗として報告する。
  // ここで打ち切ると、同じrunの他のジョブが記録した回復まで起票されない。
  const missing = [...expected].filter((profile) => !usable.some((artifact) => profileFromArtifactName(artifact.name) === profile));
  for (const profile of missing) source.summary(`missing report artifact for ${profile}`);
  const reports = options.reports || await collectFromArtifacts({ github, owner, repo, artifacts: usable });
  for (const report of reports) {
    const profile = profileFromArtifactName(report.artifactName) || report.manifest.profile;
    report.jobUrl = jobUrls.get(profile) || '';
  }
  return { groups: aggregateManifests(reports, source), fatal: missing.length > 0 ? `missing report artifact for ${missing.join(', ')}` : '' };
}

async function run(options) {
  const github = options.github;
  const owner = options.owner || options.context?.repo?.owner;
  const repo = options.repo || options.context?.repo?.repo;
  const runId = String(options.sourceRunId || options.context?.payload?.workflow_run?.id || options.context?.repo?.run_id || '');
  const attempt = String(options.sourceAttempt || options.context?.payload?.workflow_run?.run_attempt || '1');
  if (!github || !owner || !repo || !/^\d+$/.test(runId) || !/^\d+$/.test(attempt)) throw new Error('source run and repository are required');
  const sourceRun = (await github.rest.actions.getWorkflowRun({ owner, repo, run_id: Number(runId) })).data;
  // workflow_runの workflows: と同じく、workflowの表示名に依存する。pathも合わせて確認する。
  if (sourceRun.name !== WORKFLOW_CONTRACT.name || (sourceRun.path && workflowPath(sourceRun.path) !== WORKFLOW_CONTRACT.path)) {
    throw new Error('source run is not a supported workflow');
  }
  if (sourceRun.status && sourceRun.status !== 'completed') throw new Error('source run has not completed');
  if (sourceRun.run_attempt && String(sourceRun.run_attempt) !== attempt) throw new Error('source run attempt does not match requested attempt');
  const source = {
    owner, repo, runId, attempt, event: sourceRun.event || '', ref: sourceRun.head_branch || sourceRun.head_sha || '',
    apiHeadSha: sourceRun.head_sha || '', prUrl: sourceRun.pull_requests?.[0]?.html_url || '', runUrl: sourceRun.html_url || `https://github.com/${owner}/${repo}/actions/runs/${runId}`,
    commitUrl: sourceRun.head_sha ? `https://github.com/${owner}/${repo}/commit/${sourceRun.head_sha}` : '',
    summary: (message) => options.core?.warning?.(message),
  };
  const { groups, fatal } = await collectCI({ github, owner, repo, runId, attempt, source, options });
  const fileIssues = options.fileIssues ?? true;
  const issues = fileIssues ? await listIssues(github, owner, repo) : null;
  const results = [];
  const skipped = [];
  const notFiled = [];
  for (const group of groups) {
    if (!fileIssues) {
      results.push({ title: group.title, action: 'not-filed' });
      notFiled.push(group.title);
      continue;
    }
    const canCreate = results.filter((item) => item.action === 'created').length < MAX_ISSUES_PER_RUN;
    const action = await upsertGroup({ github, owner, repo, group, source, issues, settleMs: options.settleMs, canCreate });
    if (action === 'over-limit') {
      skipped.push(group.title);
      continue;
    }
    results.push({ title: group.title, action });
  }
  const summary = `flaky reports: ${results.length} issue(s); ${results.filter((item) => item.action === 'created').length} created, ${results.filter((item) => item.action === 'commented' || item.action === 'reopened-commented').length} commented`;
  if (options.core?.summary) {
    const writer = options.core.summary.addHeading('Flaky test reports').addRaw(`${summary}\n`);
    if (!fileIssues) writer.addRaw(`Not filed (file-issues=false):\n${notFiled.map((title) => `- ${title}`).join('\n') || '- none'}\n`);
    if (skipped.length > 0) writer.addRaw(`Not filed (over the ${MAX_ISSUES_PER_RUN} issue limit for one run):\n${skipped.map((title) => `- ${title}`).join('\n')}\n`);
    await writer.write();
  }
  if (fatal) throw new Error(fatal);
  return { source, results, groups, skipped, fileIssues, notFiled };
}

module.exports = {
  PROFILE_CONTRACTS,
  WORKFLOW_CONTRACT,
  CI_UPLOAD_STEP,
  aggregateManifests,
  buildIssueBody,
  buildIssueComment,
  issueTitle,
  marker,
  readZipManifest,
  run,
  upsertGroup,
  validateManifest,
};
