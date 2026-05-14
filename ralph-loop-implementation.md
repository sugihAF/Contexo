# Ralph-Loop Orchestrated Development

## Purpose

This workflow instructs the AI to implement multi-story features using iterative
`/ralph-loop` cycles. Each user story runs in its own ralph-loop where the AI
implements, tests, and iterates until the story verifiably passes. Sub-agents
handle architecture, implementation, and review.

## Placement

- Save this file at: `.claude/workflows/ralph-loop-orchestrator.md`
- Reference it in your project's `CLAUDE.md` with:
  ```
  ## Feature Implementation
  When asked to implement a feature with multiple stories, follow the workflow in
  `.claude/workflows/ralph-loop-orchestrator.md` exactly.
  ```

## Trigger

Follow this workflow when the user says any of:
- "implement [feature] with ralph-loop"
- "use ralph to build [feature]"
- "ralph-loop this feature"
- "follow this guide" or "execute using this guide" (with this file attached)
- Or when the user provides a plan/PRD and asks for iterative implementation

## Entry Point: Plan Already Exists vs Starting Fresh

**If you already have a plan from the current conversation** (the user discussed
features, created a plan, and is now asking you to execute it with this guide):
- Skip Phase 1 Steps 1-2 (exploration and story creation) — you already did that
- Start at Phase 1 Step 3: convert the existing plan into the story tracking JSON format
- Map each feature/task from the conversation into a story with:
  - `id`, `title`, `description` from the discussed plan
  - `acceptance` criteria — extract testable assertions from the discussion, or ask the user
  - `dependencies` — based on the order you discussed
- Then continue to Phase 1 Step 4 (task list) and Step 5 (approval gate)
- Then proceed to Phase 2 (ralph-loop execution)

**If starting fresh** (no prior plan in conversation):
- Start at Phase 1 Step 1 and follow everything in order

## Approval Mode

The guide has an approval gate at Phase 1 Step 5. This controls whether the AI
pauses for user confirmation before executing.

**Auto-approve (skip the gate)** when ANY of these are true:
- The user said "execute", "go ahead", "just do it", "proceed", or similar action words
- The user said "follow this guide and execute" (implies approval to run)
- The user's prompt combines both the feature request AND the guide reference in one message
- The session is non-interactive (running via `claude -p` with no way to respond)

**Pause for approval** when:
- The user only asked to "plan" or "prepare" without execution language
- The user is in an interactive session and hasn't indicated they want autonomous execution
- The requirements are ambiguous and need clarification

When auto-approved: show a brief plan summary (story list + dependency order), then
immediately proceed to Phase 2 without waiting for a response.

---

## How Ralph-Loop Actually Works

Understanding this mechanism is critical. Without it, the loop silently fails.

### The Loop Mechanism

```
User invokes: /ralph-loop "short prompt" --completion-promise "DONE" --max-iterations 10
    │
    ▼
setup-ralph-loop.sh runs:
    → Creates .claude/ralph-loop.local.md (state file with YAML frontmatter)
    → Contains: iteration count, max_iterations, completion_promise, prompt text
    │
    ▼
AI works on the task (reads files, writes code, runs tests)
    │
    ▼
AI finishes its response (session "stop" event fires)
    │
    ▼
Stop hook (stop-hook.ps1 on Windows / stop-hook.sh on Mac/Linux) fires:
    1. Checks if .claude/ralph-loop.local.md exists → if not, allow exit
    2. Reads the YAML frontmatter (iteration, max_iterations, completion_promise)
    3. Reads the last assistant message from the session transcript
    4. Searches for <promise>EXACT_TEXT</promise> in the assistant's output
    5. If promise FOUND and matches → deletes state file → session ends normally
    6. If promise NOT FOUND → blocks exit → re-feeds the same prompt as new turn
    7. If iteration >= max_iterations → deletes state file → session ends
    │
    ▼
Loop continues: AI receives the same prompt again, sees its previous work in
the files, debugs failures, and iterates until tests pass.
```

### Critical Constraints

1. **The stop hook fires at SESSION END, not between chat messages.**
   Ralph-loop is designed for autonomous execution where the AI works alone.
   If you are in an interactive chat and sending messages between the AI's
   responses, the stop hook may not trigger correctly. For reliable looping,
   let the AI work without interruption after starting the loop.

2. **Keep `/ralph-loop` prompts SHORT (under 200 characters of plain text).**
   The setup script's bash argument parser breaks with long text, special
   characters, quotes, or multi-line content. Put detailed instructions in
   a prompt file and reference it in the short prompt.

3. **The `<promise>` tag must appear in the AI's text output.**
   The stop hook reads the session transcript and searches for
   `<promise>EXACT_TEXT</promise>`. The text inside must match the
   `--completion-promise` value exactly. Do NOT output the promise unless
   the acceptance criteria are genuinely met.

4. **State file = loop is active.**
   The file `.claude/ralph-loop.local.md` existing means the loop is active.
   Deleting it (or running `/cancel-ralph`) stops the loop. If the file is
   missing, the stop hook allows normal exit.

### Verify Ralph-Loop is Installed

Before starting, check the plugin is present:
```
Glob(pattern="**/ralph-loop/**", path="~/.claude/plugins")
```

If not found, the user needs to install the ralph-loop plugin for Claude Code.

---

## Phase 1: Explore & Plan

### Step 1: Explore the codebase

Before writing ANY code, understand what exists.

**Action:** Launch a `feature-dev:code-explorer` agent via the Task tool:
```
Task(subagent_type="feature-dev:code-explorer", prompt="Explore the codebase and report:
1. Project structure and key directories
2. Existing patterns (imports, naming, architecture)
3. Test framework in use and test command
4. Build/run commands
5. Any existing code relevant to [FEATURE]")
```

Wait for the explorer to finish. Read its findings.

### Step 2: Break the feature into stories

Create user stories with these rules:
- Each story must be **independently testable** (has clear pass/fail criteria)
- Each story should be **small** (implementable in 1-3 files)
- Stories must have **explicit acceptance criteria** written as test assertions
- Identify **dependencies** (which stories must finish before others can start)
- **Each story MUST have its own dedicated test file** (e.g., `tests/test_story1_tokenizer.py`).
  This ensures each ralph-loop runs only its story's tests, not the entire suite.
- Each story's acceptance criteria should end with: "All N tests in tests/test_X.py pass"
- Write ALL test files during Phase 1 before starting Phase 2. Tests are the
  specification — they must exist before implementation begins.

### Step 3: Create the story tracking file

Create `docs/stories/<feature-slug>.json`:

```json
{
  "feature": "Feature Name",
  "created": "YYYY-MM-DD",
  "stories": [
    {
      "id": 1,
      "title": "Short descriptive title",
      "description": "What this story implements",
      "acceptance": [
        "Test X passes: [specific assertion]",
        "Function Y exists and handles Z",
        "All N tests in tests/test_X.py pass"
      ],
      "dependencies": [],
      "status": "pending",
      "files_modified": [],
      "ralph_iterations": 0
    }
  ]
}
```

### Step 4: Create task list for visibility

Use `TaskCreate` for each story so progress is visible:
```
TaskCreate(subject="Story 1: [title]", description="[description + acceptance]")
TaskCreate(subject="Story 2: [title]", description="[description + acceptance]")
```

Set up dependencies with `TaskUpdate(addBlockedBy)` where needed.

### Step 5: Present plan and check approval

Show the user:
- The story list with descriptions and acceptance criteria
- The dependency graph
- Implementation order (which stories run in parallel, which are sequential)

**Check the Approval Mode section above:**
- If auto-approved → show brief summary, then proceed directly to Phase 2
- If not auto-approved → STOP and wait for user approval before Phase 2

---

## Phase 2: Implement Stories via Ralph-Loop

Process stories in dependency order. Each story gets its own ralph-loop cycle.

### STRICT RULES — Do NOT Skip These

These rules are non-negotiable. Follow them even if it seems inefficient.

1. **ONE story per ralph-loop.** Never implement two stories in the same loop.
   Even if stories share the same file or class, implement only what the current
   story's acceptance criteria require. Do NOT look ahead at future stories.

2. **Every story MUST go through `/ralph-loop`.** Do not implement a story by
   directly writing code outside of a ralph-loop. The loop is the mechanism —
   always invoke it via the Skill tool.

3. **Only run the CURRENT story's tests.** Each story prompt must specify the
   exact test file(s) for THAT story only (e.g., `tests/test_tokenizer.py`),
   not the entire test suite. The promise is earned by passing that story's
   tests, not all tests.

4. **Commit after each story.** After a ralph-loop ends and tests pass, you MUST
   git commit before starting the next story. This creates a clear history.

5. **Update tracking after each story.** Update `docs/stories/<feature>.json`
   with status, files_modified, and ralph_iterations before moving on.

6. **Sequential execution.** Stories run one at a time, in dependency order.
   Do not batch, parallelize, or combine stories.

Violating these rules defeats the purpose of the workflow. The ralph-loop exists
to catch failures early and iterate. Skipping it removes that safety net.

### For each story:

#### Step A: Mark as in-progress

```
TaskUpdate(taskId="N", status="in_progress")
```

#### Step B: Write the story prompt file

**IMPORTANT:** Do NOT pass long prompts as inline `/ralph-loop` arguments.
The setup script's bash argument parser breaks with long text, special characters,
quotes, or multi-line content. Instead, write detailed instructions to a file.

Create `.claude/story-{id}-prompt.md`:

```markdown
# Story {id}: {title}

## SCOPE — Read This First

You are implementing ONLY Story {id}. Do NOT implement any other story.
Do NOT look ahead at other stories' test files or acceptance criteria.
Only touch code required by THIS story's acceptance criteria below.

## Description
{description}

## Acceptance Criteria
{acceptance_criteria_as_bullet_list}

## What to Do

1. Read ONLY this story's test file: {story_test_file}
   Do NOT read other stories' test files.

2. Use a feature-dev:code-architect agent (via Task tool) to design the approach:
   - Which files to create or modify
   - Function/class signatures
   - How it connects to existing code

3. Implement the code:
   - Match existing patterns and conventions in the codebase
   - Write ONLY what this story needs — nothing more
   - Do not add methods, classes, or logic for future stories
   - If tests already exist, make them pass. If not, write tests first.

4. Run ONLY this story's tests (not the full suite):
   {story_test_command}

5. If ALL tests in {story_test_file} pass AND all acceptance criteria are met:
   Output exactly: <promise>STORY_{id}_COMPLETE</promise>

6. If any test FAILS:
   - Read the failure output carefully
   - Debug and fix the code
   - Do NOT output the promise tag
   - The ralph-loop will re-run this prompt and you try again

## Context
- Story test command: {story_test_command}  (e.g., python -m pytest tests/test_foo.py -v)
- Source directory: {src_dir}
- Files to read first: {relevant_file_list}
```

#### Step C: Start the ralph-loop

**Keep the `/ralph-loop` invocation SHORT.** Reference the prompt file instead:

```
/ralph-loop Implement Story {id} {title}. Read .claude/story-{id}-prompt.md for instructions. --completion-promise "STORY_{id}_COMPLETE" --max-iterations 15
```

Or via the Skill tool:
```
Skill(skill="ralph-loop", args='Implement Story {id} {title}. Read .claude/story-{id}-prompt.md for instructions. --completion-promise "STORY_{id}_COMPLETE" --max-iterations 15')
```

**After invoking**, verify the state file was created:
```
Read(file_path=".claude/ralph-loop.local.md")
```

Check that:
- `completion_promise` matches `"STORY_{id}_COMPLETE"`
- `max_iterations` is correct
- The prompt text is present (even if abbreviated)

If the state file is wrong or garbled, fix it with Edit before proceeding.

#### Step D: Work inside the ralph-loop

Once the loop is active, the AI should:

1. **Read the prompt file** `.claude/story-{id}-prompt.md` for full instructions
2. **Read ONLY this story's test file** to understand the expected API.
   Do NOT read other stories' test files — they are out of scope.
3. **Use agents** when helpful:
   - `feature-dev:code-architect` for design decisions
   - `feature-dev:code-explorer` to understand existing code
4. **Implement ONLY what this story requires** — do not add code for future stories
5. **Run ONLY this story's tests**: `python -m pytest {story_test_file} -v`
6. **If tests pass:** output `<promise>STORY_{id}_COMPLETE</promise>`
7. **If tests fail:** debug, fix, do NOT output the promise. The loop continues.

**Scope discipline:** If you notice the current story shares a file with a future
story, only add the methods/logic needed for the current story's acceptance criteria.
The next story's ralph-loop will add the rest.

#### Step E: After the loop ends — MANDATORY checklist

When the ralph-loop finishes (promise detected or max iterations reached),
complete ALL of these steps before touching the next story. Do not skip any.

1. **Verify the story passed** — run this story's tests one more time:
   ```
   Bash(command="{story_test_command}")
   ```

2. **Update the tracking file** (`docs/stories/<feature>.json`):
   - If tests pass: set `"status": "passed"`, record `files_modified` and `ralph_iterations`
   - If tests fail: set `"status": "failed"`, note what went wrong
   - Use the Edit tool to update the JSON file. This is NOT optional.

3. **Update the task**:
   ```
   TaskUpdate(taskId="N", status="completed")  // only if passed
   ```

4. **Commit the changes** (MANDATORY — do not skip):
   ```
   git add [modified files] && git commit -m "feat: implement story {id} - {title}"
   ```
   This commit creates a checkpoint. If a future story breaks something, you can
   revert to this known-good state.

5. **Verify the commit exists**:
   ```
   Bash(command="git log --oneline -1")
   ```

6. **Only NOW move to the next story** in dependency order.
   Go back to Step A for the next pending story.

---

## Phase 3: Review & Report

After ALL stories are implemented:

### Step 1: Run the code reviewer

Launch a `feature-dev:code-reviewer` agent:
```
Task(subagent_type="feature-dev:code-reviewer", prompt="Review all files modified
during this feature implementation: {list_all_modified_files}. Check for: logic errors,
missing error handling, security issues, broken cross-references, and adherence to
project conventions.")
```

### Step 2: Fix critical issues

If the reviewer finds HIGH confidence issues, fix them immediately.
Do NOT start another ralph-loop for reviewer fixes — just edit the files directly.

### Step 3: Run all tests one final time

```
Bash(command="{test_command}")
```

### Step 4: Present summary to user

```markdown
## Feature Complete: {feature_name}

| Story | Title | Status | Ralph Iterations | Files Modified |
|-------|-------|--------|-----------------|----------------|
| 1     | ...   | passed | 1               | src/foo.py     |
| 2     | ...   | passed | 3 (2 failed)    | src/bar.py     |

### Test Results
- Total tests: {N}
- All passing: Yes/No
- Test command: `{test_command}`

### Files Modified
- {complete list}

### Notes
- {decisions, edge cases, bugs found and fixed}
```

---

## Handling Failures

### Story fails after max-iterations
1. Mark story as `"status": "needs_attention"` in the tracking JSON
2. Report to user: which tests failed, what was tried
3. Ask user whether to:
   - Retry with adjusted acceptance criteria
   - Skip and continue with other stories
   - Abort the workflow

### Ralph-loop state file is garbled
If the state file has wrong values after `/ralph-loop` invocation:
1. Read `.claude/ralph-loop.local.md`
2. Fix the YAML frontmatter with Edit tool (correct the completion_promise, max_iterations)
3. Fix or append the prompt text in the body
4. Continue — the stop hook will read the corrected file

### Test infrastructure issues
If tests can't run (missing dependencies, broken framework):
1. Cancel the ralph-loop: `/cancel-ralph`
2. Fix the test infrastructure first
3. Restart the ralph-loop for the current story

### Dependency chain broken
If a story fails and other stories depend on it:
1. Mark all dependent stories as `"status": "blocked"`
2. Report the blocking chain to the user
3. Wait for user decision

---

## Troubleshooting

### "Ralph loop doesn't seem to be running"
- Check if `.claude/ralph-loop.local.md` exists. If not, the setup script failed.
- Try running with a very short prompt: `/ralph-loop Fix the bug --completion-promise DONE --max-iterations 3`
- Check the plugin is installed: look for `ralph-loop` in `~/.claude/plugins/`

### "The prompt got garbled"
- The setup script's bash parser can't handle: quotes, newlines, special characters, or long text
- **Fix:** Keep the `/ralph-loop` prompt under ~200 chars. Put details in a `.md` file.
- If already garbled: read and edit `.claude/ralph-loop.local.md` directly

### "The loop runs but never stops"
- Check that `--completion-promise` was set (read the state file's YAML frontmatter)
- Check that `--max-iterations` is set as a safety net
- The AI must output `<promise>EXACT_TEXT</promise>` — check the exact text matches
- To force stop: `/cancel-ralph` or delete `.claude/ralph-loop.local.md`

### "The loop stops immediately without iterating"
- The stop hook fires at session end. If you're chatting interactively, the hook
  may detect the promise in your conversation and exit.
- For reliable looping: start the loop and let the AI work without sending messages.
- Ralph-loop is designed for autonomous work, not interactive back-and-forth.

### "Promise is not detected"
- The stop hook reads the session transcript JSONL file for the last assistant message
- The `<promise>` tag must be in the AI's text output (not in a tool call or code block)
- The text inside must match `--completion-promise` exactly (case-sensitive)
- If the state file's `completion_promise` is `null` or `"DONE"` when it should be
  something else, the setup args were garbled — fix the state file manually

---

## Key Principles

1. **Explore before implementing.** Never write code without reading the relevant files first.
2. **One story per ralph-loop — NO EXCEPTIONS.** Each loop focuses on exactly one
   story's acceptance criteria. Never implement two stories in the same loop, even
   if they seem related. Even if the code is simple. Even if you're confident.
   The discipline is the point.
3. **Scope to THIS story only.** Inside a ralph-loop, only read/run the current
   story's test file. Do not add code for future stories. Do not run the full
   test suite. Pass THIS story's tests, commit, then move on.
4. **Short prompts, detailed files.** Keep `/ralph-loop` args short. Put details in `.claude/story-N-prompt.md`.
5. **Agents do the heavy lifting.** Use code-explorer, code-architect, and code-reviewer agents inside the loop.
6. **Promise = verified.** Only output the completion promise when tests actually pass. Never lie to exit.
7. **Verify the state file.** After invoking `/ralph-loop`, read `.claude/ralph-loop.local.md` and fix if garbled.
8. **Commit after every story.** Each story gets its own git commit. This is mandatory.
   Update the tracking JSON before committing. No batching commits.
9. **Track everything.** JSON file for story state, TaskCreate for visibility, git commits per story.
10. **User decides final pass/fail.** Stories are marked "passed" by the workflow, but the user has final say.
11. **Fix forward.** If the reviewer finds issues after all stories, fix immediately — don't defer.
12. **Let the loop run.** Don't interrupt with chat messages. Ralph-loop works best autonomously.
13. **Never shortcut the workflow.** Even if all stories are trivial, follow every
    step: write prompt file → invoke ralph-loop → work inside loop → verify →
    update tracking → commit → next story. The process IS the product.
