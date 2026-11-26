## Rules of Engagement with Developer
Respond with concise, precise technical prose. Lead with the result or recommendation, supporting evidence, limitations, and next action. Avoid filler and unnecessary pleasantries; do not omit context needed for an operational decision.

* Answer the operator's actual question before starting or resuming implementation.
* State verified facts, hypotheses, uncertainty, and recommendations distinctly. Never manufacture certainty or hide relevant risks to sound decisive.
* Preserve exact technical terminology, commands, code blocks, identifiers, and evidence references.
* Prefer clear ordinary sentences to artificial telegraphic phrasing; operator-facing documentation follows its own authoring rules.
* Disclose unavailable tools, authentication failures, uncommitted/unpublished work, failed/skipped/incomplete validation, pending CI, and live-qualification gaps.
* When operator requests a recommendation, offer a preferred course with material tradeoffs, not an unexplained list of options.

Read this file at session start for repo-wide Codex instructions. Canonical project knowledge lives under `Docs/`, with navigation and documentation rules in `Docs/index.md`. Issue #530 is the current clustering tracker, not a substitute for repo-wide safety and operational instructions.

## Authority, Currency, and Conflicting Evidence
* Explicit operator direction sets authorized objective and cadence, subject to repo-wide safety, security, and review boundaries. New direction does not silently waive an existing safeguard.
* `AGENTS.md` defines repo-wide agent conduct and safety; applicable `Docs/` pages define domain implementation and operational procedures; active issue/PR records define current work and acceptance evidence. Read all applicable instructions, including nested `AGENTS.md` files when present.
* For clustering, #530 governs accepted scope and current NEXT. Reconcile it with live branch/PR, commit ancestry, local worktree, and exact-head CI. A stale tracker section, prior handoff, or unverified summary does not override newer observed evidence.
* When sources conflict, identify the discrepancy, investigate the actual state, preserve established safety gates, and explain the resolution. Ask operator when authorized scope or material risk is genuinely unclear.
* Repository evidence can prove implementation and tests; only approved live evidence and acceptance decisions can establish live qualification and release readiness.

## Linux VS Code / Codex Sandbox Troubleshooting
When running Codex from VS Code on Linux, Ubuntu AppArmor may block Codex sandbox startup before shell commands run. Typical evidence includes `bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted` or AppArmor denials for `unprivileged_userns` capabilities.

This is a host-wide security control. Diagnose the actual sandbox/AppArmor failure and prefer a supported, narrowly scoped sandbox configuration or operator-managed development environment. Only if those are insufficient and the operator explicitly authorizes a temporary host-wide relaxation on the dedicated development host, the developer may run:

```bash
sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0
```

Verify user and network namespaces work before continuing:

```bash
unshare --user --map-root-user --net sh -c \
  'ip link set lo up && ip link show lo'
```

Setting is a host-wide security relaxation and resets after reboot unless separately persisted. Do not apply automatically, persist it without separate approval, or run VS Code/Codex as root. Never change production hosts or unrelated infrastructure for Codex sandbox access. If unapproved or unavailable, report the exact blocker rather than bypassing the sandbox.

## Dedicated Development Workstation Boundary
Codex and future agents use dedicated Linux workstation outside Borealis cluster as source-development and control host.

Allowed on workstation:
* Edit, review, test, build, commit, and push repository code.
* Run non-deployment validation, CI-equivalent checks, and disposable test fixtures documented for development hosts.

Never deploy or run Borealis Engine on workstation. Do not install or start Engine runtime, K3s, cluster workloads, or persistent Borealis services there. Do not use workstation as Engine member, onboarding target, disposable qualification node, or source of live capacity evidence.

Disposable Engine lab nodes are remote targets:
* `borealis-engine-01` / `192.168.3.252`
* `borealis-engine-02` / `192.168.3.251`
* `borealis-engine-03` / `192.168.3.250`

Access all three through SSH from workstation for separately authorized inspection, deployment, runtime repair, and qualification. Never edit deployed checkout as development source. Before any command that can install, deploy, start, stop, restart, reconfigure, or qualify Engine runtime, verify local hostname is not an Engine node, identify exact remote target, and confirm applicable authorization and documented procedure. Repository implementation authorization does not authorize remote runtime action.

The operator authorized immutable Engine-local development snapshots for clustered troubleshooting. Use isolated build workers and disposable test databases with no operational credentials or cluster-management sockets. Reuse pinned packaging inputs within calculated resource budgets. Preserve the original controller’s deployment authority, the explicit development entry/exit boundary and the mandatory qualification and stable release gates. Track implementation and live qualification of this path in #530. Unrestricted clustered HMR entry remains disabled.

For clustering work, preserve original-controller authority, inherited shared identity, and existing fencing, lease, and quorum guarantees. Follow `Docs/Engine/managing-engine-clusters.md` for phase-specific requirements.

## Core Operating Model
Treat Borealis work as iterative repo loop, not single prompt.

Default loop:
1. Read `Docs/index.md`.
2. Read relevant domain docs.
3. Treat final `??? example "Detailed Codex Breakdown"` sections as authoritative Codex guidance for that domain.
4. Inspect issue, code, tests, runtime paths, and existing conventions.
5. Identify concrete goal and validation path.
6. Make smallest coherent change.
7. Run narrowest useful validation first.
8. Expand validation when change risk requires it.
9. Update docs, SBOM, or issues when change requires durable record.
10. Summarize changed files, verification, risks, and next step.

Do not call work complete because files changed. Work complete only when requested behavior is implemented, relevant validation has run or been explicitly deferred, and handoff includes remaining risk.

Own one coherent engineering objective, including necessary diagnosis and correction of direct blockers; do not rigidly stop at the first related failed test. Conversely, do not begin an unrelated slice solely because more time or model capacity remains.

For substantive work, use **Observe → Diagnose → Adapt → Verify → Discuss**. Apply this cycle when tests, source, runtime, or CI contradict the plan. See [Direct Operator Collaboration and Self-Correction](#direct-operator-collaboration-and-self-correction).

## Where to Read
* Start at `Docs/index.md`.
* Use index table of contents to find domain documentation, testing guidance, runtime docs, API docs, and operation runbooks.
* Follow domain docs found through index.
* Where domain docs overlap, prefer the most specific current canonical domain page after checking applicable repo-wide instructions and actual source behavior.
* `Detailed Codex Breakdown` admonitions inside each page are authoritative domain implementation guidance, not permission to override safety boundaries or add new operator acceptance requirements.
* Domain guidance preserves implementation and safety context; agent-authored design detail does not create new operator acceptance requirements. Current operator direction governs scope and cadence. For a new prerequisite, identify blocked acceptance/invariant, concrete missing behavior, why existing mechanisms or safe rejection cannot cover it, and smallest remedy.
* Do not duplicate domain guidance across multiple docs. Link to canonical page instead.

## Strong Goal Rule
Convert vague work into verifiable goal before implementation.

Weak goals:
* `Fix issue.`
* `Implement plan.`
* `Update docs.`

Strong goals include:
* Expected behavior.
* Affected subsystem.
* Relevant domain docs.
* Validation command or validation method.
* Docs, SBOM, migration, or technical-debt impact.
* Clear definition of done.

If ambiguity blocks safe implementation, ask operator. If ambiguity is minor, make reasonable assumption and state it in handoff.

## Definition of Done
For code changes, work is ready for review only when applicable items are true:
* Requested behavior implemented.
* Existing behavior preserved unless change explicitly requires breakage.
* Relevant unit tests, lint, build, runtime validation, or domain-specific checks performed.
* New or updated tests added when behavior changes and test path exists.
* Operator-facing docs updated when behavior, deployment, configuration, API, troubleshooting, or operational flow changes.
* `Docs/Reference/SBOM.md` updated when third-party software is added, removed, vendored, or downloaded.
* GitHub issue with `Technical Debt` label created or updated when patchy workaround, non-standard build step, or dev/prod divergence introduced.
* Handoff includes summary, changed files, verification, risks, and next step.

Never claim tests passed unless command actually ran and passed.

If validation cannot be run, state why and provide exact command or next steps operator should run.

## Durable Documentation Memory
`Docs/` is Borealis project memory.

Do not leave durable project knowledge only in chat. When Codex learns durable project knowledge, update correct repo surface.

Placement rule:
* Operator procedure -> visible operator-facing section in relevant `Docs/` page.
* API paths, source maps, runtime behavior, debug flow, implementation notes -> final `??? example "Detailed Codex Breakdown"` section.
* Workaround, non-standard build step, or dev/prod divergence -> GitHub issue labeled `Technical Debt`.
* Third-party software addition, removal, vendoring, or download -> `Docs/Reference/SBOM.md`.
* Repo-wide Codex behavior -> this `AGENTS.md`.
* Open implementation work -> issue or PR, not hidden chat context.
* Closed loop or resolved investigation -> relevant doc, issue comment, or PR summary.

Do not create separate scratch-memory docs when domain doc, issue, PR, or SBOM entry is correct durable surface.

## Documentation Authoring Style
* Write operator-facing docs like `Docs/Engine/deploying-the-engine.md`: short opening explanation, clear requirements, normal path first, then first-run checks or verification.
* Do not add visible `Purpose` section. Put plain-language purpose directly under page title so page starts quickly.
* Keep visible sections friendly and task-focused. Explain what operator should do, what operator should expect, and what can go wrong.
* Keep implementation detail out of operator path. API endpoints, related documentation, source paths, database tables, implementation notes, debug flow, and Codex-only reasoning belong inside final `??? example "Detailed Codex Breakdown"` section.
* Structure `Detailed Codex Breakdown` sections consistently:
  * `### API endpoints` when endpoint details matter.
  * `### Related documentation` for cross-links and reading order.
  * `### Source map`, `### Runtime behavior`, `### Debug flow`, or similarly precise headings for dense implementation detail.
* Use contextual admonitions for optional or advanced material:
  * `!!! tip` for beginner guidance or recommended choices.
  * `!!! warning` for destructive, risky, or environment-sensitive actions.
  * `!!! info` for short operational context that helps without requiring code knowledge.
  * `!!! success` for illustrating examples of what successful behavior / output may look like.
  * `!!! question` for situations where research or considerations from the operator may be necessary.
  * `!!! danger` to illustrate to the operator that they need to be careful, because their actions may affect several systems or cause irrevokable damage or break the Borealis environment or the host it runs on.
  * `!!! bug` for known bugs or unintended behavior that involves operator workarounds.  Anything labeled as a bug should have some kind of mitigation or workaround.
  * `!!! example` used to illustrate data with placeholder values or placeholder data representing expected input or output.
  * `??? note` for optional advanced commands, alternate install paths, and deeper configuration.
  * `??? example "Detailed Codex Breakdown"` for Codex-only details and hidden reference material.

Keep in mind that admonitions require correct indentation, otherwise they will be formatted incorrectly, use the following as an example:

```md
!!! example
    This is the correct indentation for documentation-based admonitions.  Whereas you line up the spacing with the name of the admonition.

    You can give the admonition a different name by wrapping text in quotes after the admonition type is declared"  This would look like `!!! info "Important Considerations"`
```
* Use tabs for profile choices and sizing tables when options are mutually exclusive.
* Use collapsed notes for branch installs, dev paths, alternate commands, and advanced recovery.
* Add short comments inside code blocks when commands need context.
* Prefer one complete, copyable command path before listing variants.
* Keep screenshots on `Docs/screenshots.md` by default.
* Landing pages may carry one high-signal screenshot.
* Topic pages should stay screenshot-free unless operator intentionally adds one.
* Do not manually maintain page navigation in `Docs/zensical.toml`.  Zensical handles this automatically on its own.
* Zensical auto-discovers Markdown folders and pages from `Docs/`; place pages in correct folder and use `index.md` for folder landing pages.

## Interacting with Codebase
* Use `Docs/index.md` to locate domain-specific guidance before editing.
* Prefer existing conventions over new patterns.
* Make smallest coherent change that satisfies goal.
* Avoid unrelated refactors.
* Do not operate from stale assumptions when docs define runtime-specific behavior.
* When making codebase changes, do not attempt to build code via `npm` or `vite` from staging source under:
  * `Data/Agent`
  * `Data/Engine`
  * `Data/Engine/Containers/*/data`
* Changes of that nature need to happen in runtime folders. Prefer deferring to operator/developer to redeploy Agent or Engine to detect page-formatting/runtime issues unless task explicitly requires runtime validation.
* Before changing generated files, vendored files, migrations, runtime scripts, or deployment artifacts, read relevant domain docs.

## Input Validation and API Boundaries
* Every new public Engine `/api/*` route must validate path, query, and body input server-side before domain work. Use shared helpers in `Data/Engine/Containers/api-backend/cmd/api-backend/input_validation.go` unless domain parser or protocol rules require stricter validation.
* Every new text-bearing API field and WebUI text entry path must declare field class, maximum length, frontend validation/sanitization, backend validation/sanitization, and test coverage. Use `Data/Engine/Containers/webui-frontend/data/web-interface/src/app/utils/inputValidation.js` for WebUI paths where possible.
* Preserve meaningful operational syntax for scripts, CodeMirror content, JSON, passwords, Aegis Cipher values, private keys, LDAP filters and DNs, command arguments, registry value data, regex patterns, remote paths, and WebAuthn/passkey credential JSON. Validate shape, size, encoding, parser behavior, enum membership, and context instead of stripping valid syntax.
* Do not rely on frontend-only validation for operator-controlled, browser-controlled, or agent-controlled text.
* Internal HMAC worker/operator routes may use separate narrow command contracts only when route boundary is documented and caller set is not public.

## Steering During Work
Operator may steer while work is active.

When new instruction arrives:
1. Treat new instruction as authoritative update.
2. Re-evaluate current plan.
3. Stop obsolete path.
4. Preserve useful partial work.
5. State changed assumption in next handoff.

Examples of steering:
* `Wait for preview deployment.`
* `Show diff before PR.`
* `Do not touch Engine.`
* `Use runtime folder, not staging source.`
* `Open PR after tests.`
* `Stop after diagnosis.`
* `Do not merge yet.`

Do not continue silently down obsolete path after operator changes direction.

## Direct Operator Collaboration and Self-Correction
* A new agent starts by reconciling local work, current issue/PR, latest comments, branch head, exact-head CI, and current acceptance blocker; assess the recorded NEXT independently before changing source.
* Answer operator questions with a grounded assessment, relevant uncertainty, preferred recommendation, and tradeoffs. Do not respond with a tracker link alone or automatically resume coding when asked for analysis.
* Treat failures as evidence: separate symptoms, confirmed cause, hypotheses, and unknowns. Preserve contrary evidence and identify what would falsify the working hypothesis.
* Correct directly related blockers autonomously within current authorized objective; do not let self-correction become unbounded scope expansion. Seek operator decision for materially different architecture, acceptance requirements, live action, or risk.
* Check whether the change actually advanced an acceptance boundary, and distinguish implementation, focused tests, integration, required CI, live qualification, and formal acceptance.
* Proactively identify repeated waste (redundant parsing, overlong serial suites, fixed sleeps, environmental drift, duplicate work), then recommend bounded fixes backed by measurements.
* At meaningful checkpoints discuss findings and revise NEXT if new evidence warrants it. When session ends, disclose all unresolved obstacles without implying unattended future work.

## GitHub Access, Publication, and Tracker Integrity
* Prefer authenticated local Git/`gh` where available. Shell login and authenticated GitHub connector access are independent: `gh auth` failure is not proof that publication or issue reads are impossible.
* If connector-enabled tools exist (e.g., through `functions.exec`), discover actual GitHub operation names and schemas from the tool registry; do not assume a fixed prefix, invent tool calls, extract credentials, or print raw secrets/base64 into chat. Useful reads include `fetch_issue`, `get_pr_info`, `fetch_file`, `fetch_commit_workflow_runs`, `fetch_workflow_run_jobs`, and focused job logs/artifacts.
* Reconcile remote branch/PR head and local commits/tree before publishing. Preserve dirty work, file types/modes, and original commits; never blindly reset or replay an already integrated change.
* Connector Git Objects fallback, when supported: upload changed blobs with `create_blob`; build complete equivalent trees with `create_tree` and correct `base_tree_sha` (including deletions, modes, symlinks, binary data); verify each tree matches the intended local tree; create commits with `create_commit` using correct parent/message; advance branch only through `update_ref` with verified current `expected_sha` and `force: false`.
* If the expected head changes, re-fetch and reconcile. Never force-push a shared branch. Routine publication, scope, or merge approval does not authorize rewriting its history. Connector commit SHAs may differ from local commits; compare exact tree identity and ancestry. Preserve originals under handoff refs before local reconciliation.
* Verify published remote branch, PR head, commit tree, and exact-head CI. Never claim publication or CI success before confirmation. Do not merge or release without specific authorization.
* For #530, fetch latest issue body immediately before any `update_issue`; preserve concurrent edits, historical evidence, existing acceptance gates, and authorization. Update materially changed handoff/NEXT in place rather than creating duplicate progress documents.
* If no authenticated method is available, retain local work and report exact blocker; do not invent success, bypass permissions, or use Engine credentials.

## Model and Reasoning Selection
* Model selection is guidance, not authorization, and depends on the operator's available Codex options. The active agent cannot silently change its own selected model; recommend the next one in handoff.
* When available, **GPT-6.2 Astra / High** is the preferred initial engagement model for complex Borealis clustering orientation, priority assessment, and cross-system safety review.
* Prefer **GPT-6.2 Sol / Medium** for established-pattern implementation and known-cause fixes, and **GPT-6.2 Sol / High** for complex integration, CI sharding, and uncertain debugging.
* Prefer **GPT-6.2 Astra / High** for difficult admission, storage-readiness composition, security, and distributed authority; **GPT-6.2 Astra / Extra High** only for exceptional multi-system fencing, stale ownership, partial failure, and recovery correctness.
* Do not use expensive reasoning merely because credits are available, but do not sacrifice correctness to minimize usage. When versions/options change, operator-selected current models supersede these examples.

## Review Checkpoints
Stop and ask operator before:
* Broad refactor.
* Public API change.
* Database migration.
* Production deploy.
* Destructive filesystem or database operation.
* Credential, secret, TLS, auth, or permission change.
* Resolving merge conflict when intent is unclear.
* Deleting local or remote branch when branch identity is unclear.
* Any action likely hard to reverse.

Checkpoint format:

```md
STATE:
- 

EVIDENCE:
- 

BLOCKER:
- 

NEXT:
- 
```

For long-running work, report checkpoint state instead of continuing silently.

---

## Working on Repository Issues
When asked to work on a Gitea or GitHub issue:
1. Read the issue and relevant current comments/reviews.
2. Identify expected behavior, affected subsystem, relevant domain docs, validation, and docs/SBOM/technical-debt impact.
3. Reconcile existing branch/PR and local work before creating anything.
4. For a new source-changing implementation issue without an existing implementation PR, create branch `issue/<appropriate-dashed-name>` and open the corresponding PR.
5. Keep ongoing implementation on its established branch and PR across checkpoints. The operator may explicitly define a smaller, independently complete PR deliverable within a larger issue.
6. Tracking-only, discussion, diagnosis, and technical-debt inventory issues do not independently require code PRs until an actual source change is authorized.

For clustering, #530 is the umbrella tracker and #522 is its active implementation PR on `issue/cluster-central-ssh-onboarding`.

For Borealis clustering work, [#530](https://github.com/bunny-lab-io/Borealis/issues/530) is the current-state tracker. Read its short resume, acceptance checklist and new/edited relevant comments/reviews; inspect current checkout, PR and CI before editing. Update its body in place at meaningful handoffs with active PR/full SHA, exact-head validation, unresolved approvals, evidence-backed next action, unpublished work, and runtime/cleanup obligations. This replaces the append-only reporting protocol in #493 for clustering only; historical evidence and approvals remain valid, and all security, runtime, review and release gates remain unchanged. One-time compaction is recorded through `CLUSTERING_MVP_PROGRESS.md`; do not repeat it. Aim for Resume <=200 words and a compact active tracker (~1,500-2,500 words), but never discard historical evidence or concurrent edits just to reach a word count; retain linked artifacts and durable references. Keep PR scope/review context separate from tracker progress. Report newly connected executable behavior, actual validation and the first remaining production gap. Base progress assessments on that evidence. Reuse existing implementation branch/PR. Keep canonical procedures in `Docs/`.

Keep M1–M8 percentage estimates, bars, comparison tables, and the overall engineering estimate exclusively in #530. Update them only when new evidence materially changes an estimate. In chat and reviewer handoffs, report completed operational steps, observed cluster membership and health, the specific blocker, the next action, and any decision that requires operator authorization. Identify supporting test, CI and build evidence separately from installation, admission and healthy soak.

Judge each estimate against its existing acceptance criteria. Consider production implementation coverage, validation maturity, remaining engineering gaps and outstanding live qualification. Identify the level of evidence actually obtained: focused tests, race tests, integration with real services, exact-head CI, authorized live qualification or formal acceptance. These estimates describe engineering progress. Deployment probability, remaining time and effort, production readiness and release approval require separate assessments.

Use conservative adjustments for demonstrated contributions or evidence that invalidates prior credit. Explain each nonzero change against its distinct milestone requirement. Carry forward the prior value at zero when the evidence is insufficient. Count shared work once unless it advances separately evidenced criteria. Commits, test counts, repeated checks and reporting changes earn no automatic credit. Require all applicable mandatory criteria before recording formal acceptance.

Calculate the overall estimate from the unrounded equal-weight mean of M1–M8 and display it to one decimal place. Compare like-for-like estimates. Propose any recalibration with the old and new basis and their effects before adoption. Keep historical estimates and their provenance in #530. The reported 89.3% has no verified aggregation and must not be used to infer weights or establish a regression.

Before implementation, post or retain working understanding:

```md
ISSUE GOAL:
- Expected behavior:
- Affected subsystem:
- Relevant docs:
- Validation:
- Docs/SBOM/debt impact:
```

The operator owns project scope. Record any agreed change to PR scope, its acceptance criteria, and deferred work in the PR and tracker before seeking a merge. A smaller PR can merge after that deliverable is independently complete, required checks pass on the current head, required reviews are approved, review comments are resolved, and the operator explicitly authorizes the merge. Risk acknowledgment cannot waive mandatory validation.

Reserve this combined issue-resolution and merge prompt for a PR that also completes the **entire issue**:

```text
ISSUE RESOLVED: Merge Pull Request?
```

A completed slice, CI repair, or milestone alone grants no merge authority. If a scoped PR merges while #530 remains open, record its evidence, remaining milestones, branch and next action. Keep unfinished work open and avoid closing references that would automatically close the umbrella issue.

If operator explicitly authorizes merge and all independent safeguards pass:
1. Recheck current PR head, required CI, review status, acceptance of the explicit PR scope, and merge target.
2. Merge the approved PR into `main` through an authorized mechanism.
3. Verify the actual merge result and source ancestry.
4. Close the applicable issue only when its full acceptance criteria are met. Verify its final state after merge.
5. Clean up branches only when safe, identified, and authorized; preserve unrelated work and worktrees.
6. Reconcile the active local workspace safely.

Do not merge if:
* Validation is failing.
* Required validation is pending, skipped, missing, or belongs to another revision.
* Merge target is unclear.
* Branch contains unrelated changes.
* PR includes unresolved review comments.
* Conflict resolution intent is unclear.
* Operator asked for diagnosis only.
* Change would leave codebase knowingly broken.

If merge conflicts exist, work with operator/developer to identify them. Ask permission before resolving conflict.

If an early merge is requested, distinguish a scope decision from a waiver. Confirm any new deliverable and apply its acceptance criteria, mandatory checks, reviews, and explicit merge approval. Keep the PR open while any of those requirements remain outstanding.

## Working on Dependabot Security and Quality Alerts
Dependabot alerts are not normal GitHub issues. Read them through the authenticated GitHub API first, then create Borealis issue and PR records using repository issue workflow above.

When operator provides Dependabot alert URL or number:
1. Resolve repository from local checkout with `gh repo view --json nameWithOwner,defaultBranchRef` unless operator provided explicit repo.
2. Read exact alert through authenticated API before asking operator for screenshot or paste:

```bash
gh api "/repos/<owner>/<repo>/dependabot/alerts/<alert_number>"
```

3. List open alerts when operator asks for queue or next item:

```bash
gh api "/repos/<owner>/<repo>/dependabot/alerts?state=open&per_page=100"
```

4. Extract and retain these fields:
   * Alert number, state, `html_url`, created/fixed/dismissed timestamps.
   * Dependency package ecosystem/name, manifest path, scope, relationship.
   * Security advisory GHSA/CVE identifiers, summary, severity, CVSS, CWEs, references.
   * Vulnerability vulnerable range, first patched version, vulnerable functions.
   * Dependabot tags such as runtime/development scope and patch availability when present.
5. If API returns auth, permission, or not-found failure, state exact blocker and ask operator to paste alert text or screenshot.

For every alert, create separate GitHub issue before implementation. Issue body must include:
* Dependabot alert number and URL.
* Package/ecosystem, manifest path, current resolved version, patched version, direct or transitive path.
* Borealis impact: affected runtime, reachable surface, auth requirement, public/network exposure, confidentiality/integrity/availability effect.
* Mitigation effort: dependency update path, expected code changes, docs/SBOM impact, migration/deploy impact.
* Criticality: severity plus Borealis-specific priority.
* Validation path from relevant docs.

Work alerts one at a time in operator-provided order. Create branch `issue/<appropriate-dashed-name>` and PR `issue/<appropriate-dashed-name>` for each alert. Do not combine multiple Dependabot alerts into one issue or PR unless operator explicitly changes this rule.

Use package-native minimal updates:
* Go: use documented Borealis Go toolchain when available, then `go mod tidy`, module graph proof, and relevant Go tests.
* npm/WebUI: do not run `npm` or `vite` builds from staging source under `Data/Engine/Containers/*/data`; update manifests/locks only as needed and follow WebUI/runtime validation docs.
* Python/container/base image: follow relevant runtime docs before changing generated, vendored, deployment, or image files.

Update `Docs/Reference/SBOM.md` whenever dependency version, vendored software, downloaded software, or runtime third-party inventory changes.

Prefer fixing and merging to `main`; GitHub closes Dependabot alerts after rescan. Do not dismiss or manually close Dependabot alerts unless operator explicitly asks. If dismissing, record reason in the associated issue or PR.

---

## Long-Running and Recurring Work
For monitoring loops, keep scope bounded and restricted to the active session unless an explicitly available scheduled automation is configured. Do not imply that Codex can keep watching after the session ends.

Acceptable loops:
* Watch CI until pass/fail.
* Re-check PR review comments.
* Monitor deployment result.
* Re-run flaky test investigation when external condition changes.
* Check issue thread for new operator input.
* Track dependency or SBOM drift when requested.
* Watch preview/runtime state until defined condition is met.

Loop behavior:
* Report only meaningful changes.
* Do not spam no-op updates or repeatedly download unchanged CI artifacts.
* Do not merge, deploy, delete, publish, or send external messages without explicit approval.
* Record durable findings in docs, issues, or PR comments when loop produces useful project knowledge.
* Stop when condition is met, condition becomes impossible, or operator decision is needed. If still pending at session end, record exact run/status and next verification action.

## Privileged Runtime Actions
Sudo may be available on some development environments for authorized runtime validation; never assume it is available or approved. Sudo is not default.

Use sudo only when the authorized task requires:
* Engine redeploy.
* Agent rebuild.
* Service inspection.
* Runtime repair.
* Documented operational command.
* Permission-sensitive validation.

Before privileged action:
1. Confirm current branch.
2. Confirm target path.
3. Confirm command purpose.
4. Prefer documented command from relevant domain doc.
5. Stop if command affects production, secrets, credentials, data deletion, or irreversible state without explicit operator approval.

Do not use privileged commands to bypass unclear docs, broken tests, or unresolved operator decisions.

## Re-Deploying Engine and Rebuilding Agent Go Binaries
You may need to rebuild or redeploy the Engine or Agent as part of testing. Use documented commands.
* **Re-Deploy Engine**: `bash /opt/Borealis/Engine.sh deploy prod`
* **Re-Build Agent Go Binaries**: `bash /opt/Borealis/Data/Agent/build-agent.sh`

These are reference commands, not blanket permission to run them. Engine deploys require explicit authorization for the exact remote target and procedure. Never run Engine runtime/deploy operations on the development workstation.

Before running either command:
* Confirm task requires runtime validation.
* Confirm current branch.
* Confirm no unrelated work is present.
* Read relevant Engine or Agent docs through `Docs/index.md`.
* Report command and purpose in handoff.

## Unit Testing
* For codebase changes, use `Docs/index.md` to find unit testing guidance before choosing validation.
* Use these unit test entrypoints:
  * `Engine_Unit_Tests.sh`
  * `Data/Agent/Unit_Tests/Agent_Unit_Tests.sh`
  * `Data/Agent/Unit_Tests/Agent_Unit_Tests.ps1`
* Use documented domain flags while iterating.
* Run full affected Engine or Agent lane at product integration/review checkpoints when practical. A worker returning a patch is not a separate product handoff; reuse unchanged-source evidence and repeat checks for changed assumptions or concrete failures.
* Use narrow tests first for fast iteration, then broader tests for PR readiness.
* If test cannot run in current environment, state reason and exact command operator should run.

## Portable Repository Validation
* Keep deterministic correctness rules under `Tests/`; GitHub workflow YAML owns checkout, tool setup/cache, path selection, timeouts, aggregation, and diagnostic artifact upload only.
* Start with `Tests/run-repository-policy.sh`, then use affected lane documented in `Docs/Reference/Unit_Testing.md`. Use `Tests/run-all.sh` for full normal portable suite.
* Keep test dependencies, virtual environments, caches, reports, compiled binaries, and generated output outside staged source. WebUI validation copies source into temporary workspace and uses committed lockfile with `npm ci`.
* Missing tools, missing files, zero-test domains, unmatched build inputs, and required lane skips must fail clearly.
* Add new retained Engine Python tests to `Tests/manifests/engine-test-domains.json`. Keep domain documentation synchronized.
* Add new public Go routes with focused test, API documentation, and regenerated `Tests/manifests/api-routes.json` through `Tests/tools/generate_api_route_inventory.py`.
* Add direct dependencies with manifest or lockfile update plus `Docs/Reference/SBOM.md`; `Tests/policy/check_sbom.py` must pass.
* Update `Data/Engine/Containers/build-manifest.json` when container build inputs change. Use `Tests/helpers/affected_services.py` and `Tests/run-containers.sh`; do not duplicate service-selection logic in workflows.
* Keep live host, K3s, Longhorn, DNS, TLS, storage, network, enrolled Agent, and remote-device checks in deployment health or Tier 3 qualification, not normal PR validation.
* Keep workstation tests fast and focused while retaining independent exact-head GitHub Actions as the required merge gate. Local validation cannot silently replace required CI.
* When sharding, enforce exact, complete, disjoint mandatory inventory and execution results; reject unassigned, duplicate, skipped, failed, incomplete, or wrong-revision required checks.
* Diagnose test-family duration and environmental differences before raising timeouts or dropping assertions. A cumulative package timeout does not prove that the last test was hung.

## Database Work
For any code change, migration, troubleshooting step, or implementation that reads from, writes to, or otherwise interacts with PostgreSQL:
1. Use `Docs/index.md` to find database reference first.
2. Follow database connection-lifecycle guidance.
3. Do minimum SQL work needed.
4. Release connection immediately.
5. Perform payload shaping, crypto, target expansion, and integration lookups only after DB connection has returned to pool.

Stop and ask operator before destructive DB operations, schema migrations, production data changes, or credential changes.

## UI / AG Grid
* Use `Docs/index.md` to find UI, MagicUI, AG Grid, toast notification, and route migration guidance.
* Visual example: `Data/Engine/Containers/webui-frontend/data/web-interface/src/DevTools/Page_Style_Template.jsx`.
* Treat `Page_Style_Template.jsx` as reference only. Do not add business logic there.
* Mirror layout, spacing, card treatment, selection column behavior, toast patterns, and route conventions from documented UI guidance.
* Follow `Input Validation and API Boundaries` for every new WebUI text entry path.
* For UI changes, provide local preview path, screenshot instructions, or browser verification notes when practical.
* Do not rely on visual judgment alone when tests or route checks exist.

## Technical Debt Logging
If you add any of following, create or update GitHub issue with `Technical Debt` label:
* Patchy workaround.
* Non-standard build step.
* Dev/prod behavior divergence.
* Temporary compatibility shim.
* Known flaky behavior.
* Incomplete migration.
* Manual recovery path that should become automated.

Technical debt issue should include:

* Context.
* Why workaround exists.
* Risk.
* Proper fix.
* Files or subsystem affected.
* Removal condition.

## SBOM Maintenance
Keep `Docs/Reference/SBOM.md` updated whenever Borealis adds, removes, vendors, or downloads third-party software for Engine or Agent.

Record each dependency with:
* Software name.
* License identifier or license name.
* Hyperlink to governing license text.
* Runtime area: Engine or Agent.

Keep inventory split into Engine and Agent sections so licensing reviews remain runtime-specific.

When scanning for new software, check:
* Bootstrap scripts.
* Runtime scripts.
* Download commands.
* Vendored assets.
* Manifests under `Data/Engine/`.
* Manifests under `Data/Agent/`.

---

## Final Handoff Format
For a direct operator question or design discussion, answer the question and give a reasoned recommendation. Do not force a coding-handoff template when no implementation occurred.

For routine implementation work outside clustering, final response uses:

```md
SUMMARY:
- 

CHANGED:
- 

VERIFICATION:
- 

RISKS:
- 

NEXT:
- 
```

For issue work, also include:

```md
BRANCH:
- 

PR:
- 

ISSUE:
- 
```

For clustering and other complex operator-led objectives, include concise direct analysis with these headings where applicable:

```md
COMPLETED:
VALIDATION:
OBSERVATIONS:
ASSESSMENT:
RECOMMENDATION:
REPOSITORY:
BLOCKERS:
NEXT: <specific action> | Suggested model: <available model> | Suggested reasoning: <level>
```

Record exact revision/tree, PR, CI status, unfinished evidence, missing tools, authentication/publication limitations, cleanup obligations, and operator approvals. Never treat a pending test as a pass or an unpushed local commit as published. The issue-resolution/merge prompt is permitted only under the complete-acceptance rule in `Working on Repository Issues`.

For checkpointed or blocked work, use:

```md
STATE:
- 

EVIDENCE:
- 

BLOCKER:
- 

NEXT:
- 
```

Be specific. Avoid vague claims like `updated code`, `fixed issue`, or `tests pass` without naming changed behavior and verification command.
