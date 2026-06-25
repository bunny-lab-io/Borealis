## Rules of Engagement with Developer
Respond like smart caveman. Cut all filler, keep technical substance.

* Drop articles (`a`, `an`, `the`) and filler (`just`, `really`, `basically`, `actually`).
* Drop pleasantries (`sure`, `certainly`, `happy to`).
* No hedging. Fragments fine. Short synonyms.
* Technical terms stay exact.
* Code blocks unchanged.
* Pattern: `[thing] [action] [reason]. [next step].`

Use this file as entrypoint for Codex instructions. Full knowledgebase lives under `Docs/`, with navigation and documentation rules in `Docs/index.md`.

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

## Where to Read
* Start at `Docs/index.md`.
* Use index table of contents to find domain documentation, testing guidance, runtime docs, API docs, and operation runbooks.
* Follow domain docs found through index.
* Where docs overlap, domain page wins.
* `Detailed Codex Breakdown` admonitions inside each page are authoritative agent guidance.
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
* Do not manually maintain page navigation in `zensical.toml`.  Zensical handles this automatically on its own.
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

## Review Checkpoints
Stop and ask operator before:
* Broad refactor.
* Public API change.
* Database migration.
* Production deploy.
* Destructive filesystem or database operation.
* Credential, secret, TLS, auth, or permission change.
* Force push.
* Merge with failing or unverified validation.
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
When asked to work on Gitea or GitHub issue:
1. Read issue to understand context.
2. Identify expected behavior, affected subsystem, relevant domain docs, validation command, and docs/SBOM/technical-debt impact.
3. Create repo branch named `issue/<appropriate-dashed-name>`.
4. Open pull request named `issue/<appropriate-dashed-name>`.
5. Perform all issue-related work on that branch.
6. Keep same branch and PR for ongoing issue until issue resolved, regardless of commit count.

Every issue has corresponding pull request. No exceptions, no matter how small request is.

Before implementation, post or retain working understanding:

```md
ISSUE GOAL:
- Expected behavior:
- Affected subsystem:
- Relevant docs:
- Validation:
- Docs/SBOM/debt impact:
```

When work is complete, tell operator exactly:

```text
ISSUE RESOLVED: Merge Pull Request?
```

If operator responds with affirmative instruction:
1. Confirm validation state.
2. Close issue.
3. Merge pull request into `main`.
4. Delete remote branch associated with pull request.
5. Delete local branch associated with pull request.
6. Switch active local workspace to `main`.
7. Sync all changes so local workspace is up-to-date with recently merged changes.

Do not merge if:
* Validation is failing.
* Validation was not run and risk is non-trivial.
* Merge target is unclear.
* Branch contains unrelated changes.
* PR includes unresolved review comments.
* Conflict resolution intent is unclear.
* Operator asked for diagnosis only.
* Change would leave codebase knowingly broken.

If merge conflicts exist, work with operator/developer to identify them. Ask permission before resolving conflict.

If developer asks to merge pull request early, confirm exactly:

```text
PR MERGE REQUESTED: Are you sure?
```

If operator confirms, merge PR before originally tasked work is complete only when operator explicitly accepts risk. Urge operator to finish changes before merge if merge would leave codebase broken or unsafe.

---

## Long-Running and Recurring Work
For monitoring loops, keep scope bounded.

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
* Do not spam no-op updates.
* Do not merge, deploy, delete, publish, or send external messages without explicit approval.
* Record durable findings in docs, issues, or PR comments when loop produces useful project knowledge.
* Stop when condition is met, condition becomes impossible, or operator decision is needed.
W
## Privileged Runtime ActionsWWW
Sudo is available in development environment for required runtime validation. Sudo is not default.

Use sudo only when task requires:WW
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
* Run full affected Engine or Agent lane before handoff when practical.
* Use narrow tests first for fast iteration, then broader tests for PR readiness.
* If test cannot run in current environment, state reason and exact command operator should run.

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
For implementation work, final response must use:

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

If issue appears resolved, end with exact operator prompt:

```text
ISSUE RESOLVED: Merge Pull Request?
```

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
