#!/usr/bin/env node
// Upstream-component tests, not a hosted-bot E2E. The actual unmodified writer
// and reader run in a VM; only platform IO and unrelated services are doubled.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFile, writeFile } from 'node:fs/promises';
import { stripTypeScriptTypes } from 'node:module';
import vm from 'node:vm';

const VERSION = '44.148.3';
const BLOB = '26a56b44e2377953d59930757f9e5e029b49e063';
const [sourcePath, reportPath] = process.argv.slice(2);
assert.ok(sourcePath && reportPath, 'usage: script upstream.ts report.json');
const source = await readFile(sourcePath);
assert.ok(source.length > 1000 && source.length <= 131072, 'source size budget');
const blob = createHash('sha1').update(`blob ${source.length}\0`).update(source).digest('hex');
assert.equal(blob, BLOB, 'review pinned upstream source changes; never bypass the hash');
const code = stripTypeScriptTypes(source.toString('utf8'), { mode: 'strip' });
const configured = JSON.parse(await readFile(new URL('../renovate.json', import.meta.url), 'utf8'));
assert.equal(configured.dependencyDashboard, true);
assert.equal(configured.dependencyDashboardAutoclose, true);
assert.equal(configured.dependencyDashboardApproval, true);
assert.equal(configured.automerge, false);
const report = {
  scope: 'actual pinned upstream dashboard reader/writer; memory-only platform, not hosted E2E',
  renovateVersion: VERSION, sourceGitBlob: blob,
  sourceSHA256: createHash('sha256').update(source).digest('hex'),
  cases: [],
};

function unexpected() { throw new Error('unexpected external dependency call'); }

async function harness({ existing = true, dryRun = undefined } = {}) {
  const state = { open: existing, body: '', calls: [] };
  const globals = new Map([['dryRun', dryRun]]);
  const config = {
    ...structuredClone(configured), repository: 'fixture/dashboard',
    repoIsOnboarded: true, dependencyDashboardTitle: 'Dependency Dashboard',
    dependencyDashboardChecks: {}, dependencyDashboardOSVVulnerabilitySummary: 'none',
  };
  const platform = {
    ensureIssueClosing: async (title) => {
      assert.equal(title, config.dependencyDashboardTitle);
      state.calls.push({ action: 'close', title }); state.open = false;
    },
    ensureIssue: async (issue) => {
      assert.equal(issue.title, config.dependencyDashboardTitle);
      state.calls.push({ action: 'ensure', body: issue.body });
      state.open = true; state.body = issue.body;
    },
    findIssue: async () => state.open ? { number: 1, body: state.body } : null,
    getIssue: async () => state.open ? { number: 1, body: state.body } : null,
    maxBodyLength: () => 65536,
    massageMarkdown: (text) => text,
  };
  // These doubles do not implement any lifecycle/approval decisions. Those
  // decisions and branch categorization come exclusively from the pinned file.
  const doubles = {
    '@sindresorhus/is': {
      isNonEmptyArray: (x) => Array.isArray(x) && x.length > 0,
      isNonEmptyObject: (x) => x !== null && typeof x === 'object' && Object.keys(x).length > 0,
      isNonEmptyString: (x) => typeof x === 'string' && x.length > 0,
      isNullOrUndefined: (x) => x === null || x === undefined,
      isTruthy: Boolean,
    },
    luxon: { DateTime: { fromISO: unexpected } },
    '../../config/global.ts': { GlobalConfig: { get: (key) => globals.get(key) } },
    '../../logger/index.ts': { logger: { debug() {}, info() {}, warn: unexpected, error: unexpected } },
    '../../modules/platform/index.ts': { platform },
    '../../util/array.ts': { coerceArray: (x) => Array.isArray(x) ? x : [] },
    '../../util/emoji.ts': { emojify: (x) => x },
    '../../util/regex.ts': { regEx: (pattern, flags) => new RegExp(pattern, flags) },
    '../../util/string.ts': { coerceString: (x) => typeof x === 'string' ? x : '' },
    '../../util/template/index.ts': { compile: (x) => x },
    './common.ts': { extractRepoProblems: () => new Map(), replacementAlreadyExists: () => false },
    './errors-warnings.ts': { getDepWarningsDashboard: () => '' },
    './package-files.ts': { PackageFiles: { getDashboardMarkdown: () => '' } },
    './process/vulnerabilities.ts': { Vulnerabilities: { create: unexpected } },
  };
  const context = vm.createContext({});
  const mod = new vm.SourceTextModule(code, { context, identifier: `renovate@${VERSION}/dependency-dashboard.ts` });
  await mod.link((name) => {
    assert.ok(Object.hasOwn(doubles, name), `unreviewed upstream import ${name}`);
    const values = doubles[name];
    return new vm.SyntheticModule(Object.keys(values), function () {
      for (const [key, value] of Object.entries(values)) this.setExport(key, value);
    }, { context, identifier: name });
  });
  await mod.evaluate({ timeout: 5000 });
  return {
    state, config,
    write: (branches, files = {}) => mod.namespace.ensureDependencyDashboard(config, branches, files, { result: 'no-migration' }),
    read: () => mod.namespace.readDashboardBody(config),
  };
}

const branch = (result, extra = {}) => ({
  branchName: 'renovate/reviewed-update', prTitle: 'Update reviewed dependency',
  result, dependencyDashboardApproval: true,
  upgrades: [{ depName: 'github.com/egoist/mygo', newValue: 'fixture-only' }], ...extra,
});
async function check(name, run) {
  await run(); report.cases.push({ name, status: 'passed' });
  console.log(`PASS ${name}`);
}

try {
  await check('empty_queue_closes_existing_dashboard', async () => {
    const h = await harness(); await h.write([]);
    assert.equal(h.state.open, false);
    assert.equal(h.state.calls.length, 1); assert.equal(h.state.calls[0].action, 'close');
  });
  await check('new_pending_work_ensures_dashboard_and_requires_explicit_approval', async () => {
    const h = await harness(); await h.write([]); assert.equal(h.state.open, false);
    const pending = [branch('needs-approval')]; const before = JSON.stringify(pending);
    h.state.calls = []; await h.write(pending);
    assert.equal(h.state.calls.length, 1); assert.equal(h.state.calls[0].action, 'ensure');
    assert.match(h.state.body, /## Pending Approval/);
    assert.ok(h.state.body.includes(' - [ ] <!-- approve-branch=renovate/reviewed-update -->'));
    assert.equal(JSON.stringify(pending), before, 'writer must not approve or mutate updates');
    await h.read(); assert.equal(h.config.dependencyDashboardChecks['renovate/reviewed-update'], undefined);
    h.state.body = h.state.body.replace(' - [ ] <!-- approve-branch=', ' - [x] <!-- approve-branch=');
    await h.read(); assert.equal(h.config.dependencyDashboardChecks['renovate/reviewed-update'], 'approve');
    assert.equal(h.config.automerge, false); assert.equal(h.config.dependencyDashboardApproval, true);
    h.state.calls = []; await h.write([]); assert.equal(h.state.open, false);
    assert.equal(h.state.calls[0].action, 'close');
  });
  for (const [result, heading, extra] of [
    ['error', 'Errored', {}], ['pr-limit-reached', 'Rate-Limited', {}],
    ['branch-limit-reached', 'Rate-Limited', {}], ['not-scheduled', 'Awaiting Schedule', {}],
    ['pending', 'Pending Status Checks', {}], ['already-existed', 'PR Closed (Blocked)', {}],
    ['needs-pr-approval', 'PR Creation Approval Required', {}], ['done', 'Open', { prNo: 42 }],
  ]) {
    await check(`${result}_is_visible_not_closed`, async () => {
      const h = await harness({ existing: false }); await h.write([branch(result, extra)]);
      assert.equal(h.state.open, true); assert.equal(h.state.calls.length, 1);
      assert.equal(h.state.calls[0].action, 'ensure'); assert.ok(h.state.body.includes(`## ${heading}`));
    });
  }
  for (const mode of ['deprecated', 'replacement']) {
    await check(`${mode}_without_branches_keeps_dashboard`, async () => {
      const h = await harness(); const dep = { depName: 'fixture', packageName: 'fixture' };
      if (mode === 'deprecated') dep.deprecationMessage = 'deprecated fixture';
      else dep.updates = [{ updateType: 'replacement', newName: 'fixture-new' }];
      await h.write([], { gomod: [{ packageFile: 'go.mod', deps: [dep] }] });
      assert.equal(h.state.calls[0].action, 'ensure'); assert.ok(h.state.body.includes('## Deprecations / Replacements'));
    });
  }
  await check('completed_automerged_work_does_not_keep_empty_dashboard', async () => {
    const h = await harness(); await h.write([branch('automerged')]);
    assert.equal(h.state.calls[0].action, 'close');
  });
  await check('disabled_autoclose_negative_control_retains_empty_dashboard', async () => {
    const h = await harness(); h.config.dependencyDashboardAutoclose = false;
    await h.write([]); assert.equal(h.state.open, true); assert.equal(h.state.calls[0].action, 'ensure');
  });
  await check('dry_run_never_writes_or_closes_platform_issues', async () => {
    const h = await harness({ dryRun: 'full' });
    await h.write([]); await h.write([branch('needs-approval')]);
    assert.equal(h.state.calls.length, 0);
  });
  assert.equal(report.cases.length, 15, 'all mandatory scenarios must execute');
  report.status = 'passed';
} catch (err) {
  report.status = 'failed'; report.error = String(err.stack || err);
  process.exitCode = 1; console.error(err);
} finally {
  await writeFile(reportPath, JSON.stringify(report, null, 2) + '\n');
}
