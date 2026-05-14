$ErrorActionPreference = "Continue"
$CTX = "D:\Codes\Contexo\bin\ctx.exe"
$ROOT = "D:\Codes\Contexo\e2e_test\workspace"
$REPORT = "D:\Codes\Contexo\e2e_test\e2e_report.md"

# Clean workspace
if (Test-Path $ROOT) { Remove-Item -Recurse -Force $ROOT }
New-Item -ItemType Directory -Path $ROOT -Force | Out-Null

# Initialize report
$report = @()
$report += "# CtxHub MVP-1 End-to-End Test Report"
$report += ""
$report += "**Date:** $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')"
$report += "**Platform:** Windows 11, Go 1.26.0"
$report += "**Binary:** $CTX"
$report += ""

function Run-Ctx {
    param([string]$Description, [string[]]$Args)
    $report_section = @()
    $report_section += "## $Description"
    $report_section += ""
    $cmdLine = "$CTX $($Args -join ' ')"
    $report_section += '```'
    $report_section += "$ ctx $($Args -join ' ')"

    try {
        $output = & $CTX @Args 2>&1 | Out-String
        $report_section += $output.TrimEnd()
    } catch {
        $report_section += "ERROR: $($_.Exception.Message)"
    }
    $report_section += '```'
    $report_section += ""
    return $report_section
}

function Run-HTTP {
    param([string]$Description, [string]$Method, [string]$Url, [string]$Body)
    $report_section = @()
    $report_section += "## $Description"
    $report_section += ""
    $report_section += '```'
    $report_section += "$Method $Url"
    if ($Body) { $report_section += "Body: $Body" }

    try {
        if ($Body) {
            $response = Invoke-WebRequest -Uri $Url -Method $Method -ContentType "application/json" -Body $Body -UseBasicParsing
        } else {
            $response = Invoke-WebRequest -Uri $Url -Method $Method -UseBasicParsing
        }
        $report_section += "Status: $($response.StatusCode)"
        $report_section += "Response: $($response.Content)"
    } catch {
        $e = $_.Exception
        if ($e.Response) {
            $statusCode = [int]$e.Response.StatusCode
            $sr = New-Object System.IO.StreamReader($e.Response.GetResponseStream())
            $body = $sr.ReadToEnd()
            $report_section += "Status: $statusCode"
            $report_section += "Response: $body"
        } else {
            $report_section += "Error: $($e.Message)"
        }
    }
    $report_section += '```'
    $report_section += ""
    return $report_section
}

# ===== 1. ctx init =====
$report += "---"
$report += "# Part 1: Initialization"
$report += ""
$report += Run-Ctx "1.1 Initialize project" @("init", "--root", $ROOT)

# Verify directory structure
$report += "## 1.2 Verify .ctx directory structure"
$report += ""
$report += '```'
$items = Get-ChildItem -Recurse -Name (Join-Path $ROOT ".ctx")
$report += ($items -join "`n")
$report += '```'
$report += ""

# Read config
$report += "## 1.3 Config contents"
$report += ""
$report += '```json'
$report += (Get-Content (Join-Path $ROOT ".ctx\config.json") -Raw).TrimEnd()
$report += '```'
$report += ""

# ===== 2. Capture commands =====
$report += "---"
$report += "# Part 2: Capture (Recorder Daemon)"
$report += ""

# Start capture as background process
$report += "## 2.1 Start capture (background process)"
$report += ""
$report += '```'
$report += "$ ctx capture on --client claude-code --root $ROOT"
$captureJob = Start-Process -FilePath $CTX -ArgumentList "capture","on","--client","claude-code","--root",$ROOT -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $ROOT "capture_stdout.txt") -RedirectStandardError (Join-Path $ROOT "capture_stderr.txt")
Start-Sleep -Seconds 2
$captureStdout = Get-Content (Join-Path $ROOT "capture_stdout.txt") -Raw -ErrorAction SilentlyContinue
if ($captureStdout) { $report += $captureStdout.TrimEnd() }
$report += "(Running as PID: $($captureJob.Id))"
$report += '```'
$report += ""

# Read port from state
$stateFile = Join-Path $ROOT ".ctx\capture_state.json"
$stateJson = Get-Content $stateFile -Raw | ConvertFrom-Json
$PORT = $stateJson.port

$report += "## 2.2 Capture state"
$report += ""
$report += '```json'
$report += (Get-Content $stateFile -Raw).TrimEnd()
$report += '```'
$report += ""

# Verify hooks.json
$report += "## 2.3 Generated hooks.json"
$report += ""
$report += '```json'
$hooksFile = Join-Path $ROOT ".ctx\hooks.json"
if (Test-Path $hooksFile) {
    $report += (Get-Content $hooksFile -Raw).TrimEnd()
} else {
    $report += "NOT FOUND"
}
$report += '```'
$report += ""

# Check health
$report += Run-HTTP "2.4 Health check" "GET" "http://127.0.0.1:$PORT/health" $null

# Test invalid JSON
$report += Run-HTTP "2.5 Post invalid JSON (expect 400)" "POST" "http://127.0.0.1:$PORT/event" "not valid json"

# Capture status
$report += Run-Ctx "2.6 Capture status" @("capture", "status", "--root", $ROOT)

# Capture pause
$report += Run-Ctx "2.7 Capture pause" @("capture", "pause", "--root", $ROOT)
$report += Run-Ctx "2.8 Capture status (after pause)" @("capture", "status", "--root", $ROOT)

# Capture resume
$report += Run-Ctx "2.9 Capture resume" @("capture", "resume", "--root", $ROOT)
$report += Run-Ctx "2.10 Capture status (after resume)" @("capture", "status", "--root", $ROOT)

# ===== 3. Simulate events =====
$report += "---"
$report += "# Part 3: Simulate Claude Code Hook Events"
$report += ""

# Event 1: User prompt
$event1 = @'
{
  "schema": "ctx.session_event.v1",
  "event_id": "evt-001-user-prompt",
  "ts": "2025-06-01T10:00:00Z",
  "session": {"id": "session-001", "source": "claude_code", "started_at": "2025-06-01T09:59:00Z"},
  "type": "user_message",
  "turn": 1,
  "content": {"text": "Implement a login form with email and password validation"}
}
'@
$report += Run-HTTP "3.1 Event: User prompt (turn 1)" "POST" "http://127.0.0.1:$PORT/event" $event1

# Event 2: Assistant response
$event2 = @'
{
  "schema": "ctx.session_event.v1",
  "event_id": "evt-002-assistant-response",
  "ts": "2025-06-01T10:00:30Z",
  "session": {"id": "session-001", "source": "claude_code", "started_at": "2025-06-01T09:59:00Z"},
  "type": "assistant_message",
  "turn": 2,
  "content": {"text": "I'll create a login form component with email validation using regex and password strength checking. Here's the implementation..."}
}
'@
$report += Run-HTTP "3.2 Event: Assistant response (turn 2)" "POST" "http://127.0.0.1:$PORT/event" $event2

# Event 3: User follow-up
$event3 = @'
{
  "schema": "ctx.session_event.v1",
  "event_id": "evt-003-user-followup",
  "ts": "2025-06-01T10:01:00Z",
  "session": {"id": "session-001", "source": "claude_code", "started_at": "2025-06-01T09:59:00Z"},
  "type": "user_message",
  "turn": 3,
  "content": {"text": "Add rate limiting to prevent brute force attacks"}
}
'@
$report += Run-HTTP "3.3 Event: User follow-up (turn 3)" "POST" "http://127.0.0.1:$PORT/event" $event3

# Event 4: Assistant response
$event4 = @'
{
  "schema": "ctx.session_event.v1",
  "event_id": "evt-004-assistant-response2",
  "ts": "2025-06-01T10:01:30Z",
  "session": {"id": "session-001", "source": "claude_code", "started_at": "2025-06-01T09:59:00Z"},
  "type": "assistant_message",
  "turn": 4,
  "content": {"text": "I'll add rate limiting middleware using a token bucket algorithm. After 5 failed attempts, the user gets locked out for 15 minutes."}
}
'@
$report += Run-HTTP "3.4 Event: Assistant response with rate limiting (turn 4)" "POST" "http://127.0.0.1:$PORT/event" $event4

# Event 5: Different session
$event5 = @'
{
  "schema": "ctx.session_event.v1",
  "event_id": "evt-005-session2-start",
  "ts": "2025-06-01T11:00:00Z",
  "session": {"id": "session-002", "source": "claude_code", "started_at": "2025-06-01T11:00:00Z"},
  "type": "user_message",
  "turn": 1,
  "content": {"text": "Refactor the database layer to use connection pooling"}
}
'@
$report += Run-HTTP "3.5 Event: Different session (session-002)" "POST" "http://127.0.0.1:$PORT/event" $event5

# Event 6: Event with secret (redaction test)
$event6 = @'
{
  "schema": "ctx.session_event.v1",
  "event_id": "evt-006-secret-test",
  "ts": "2025-06-01T11:01:00Z",
  "session": {"id": "session-002", "source": "claude_code", "started_at": "2025-06-01T11:00:00Z"},
  "type": "assistant_message",
  "turn": 2,
  "content": {"text": "Here is your AWS key: AKIAIOSFODNN7EXAMPLE and secret: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
}
'@
$report += Run-HTTP "3.6 Event: Contains AWS secret (redaction test)" "POST" "http://127.0.0.1:$PORT/event" $event6

# Verify JSONL files
$report += "## 3.7 Verify JSONL session files"
$report += ""
$report += '```'
$sessDir = Join-Path $ROOT ".ctx\sessions\claude_code"
if (Test-Path $sessDir) {
    $files = Get-ChildItem -Name $sessDir
    $report += "Session files:"
    foreach ($f in $files) {
        $lineCount = (Get-Content (Join-Path $sessDir $f) | Measure-Object -Line).Lines
        $report += "  $f ($lineCount events)"
    }
} else {
    $report += "Session directory not found!"
}
$report += '```'
$report += ""

# ===== 4. Session CLI commands =====
$report += "---"
$report += "# Part 4: Session Commands"
$report += ""

$report += Run-Ctx "4.1 List sessions" @("session", "ls", "--root", $ROOT)
$report += Run-Ctx "4.2 Show session-001" @("session", "show", "session-001", "--root", $ROOT)
$report += Run-Ctx "4.3 Show session-001 turns 1-2" @("session", "show", "session-001", "--turns", "1-2", "--root", $ROOT)

# ===== 5. Commit CLI commands =====
$report += "---"
$report += "# Part 5: Context Commits"
$report += ""

$report += Run-Ctx "5.1 Create commit for auth feature" @("commit", "-m", "Implement login form with email/password validation", "--feature", "auth", "--root", $ROOT)
$report += Run-Ctx "5.2 Create commit for db feature" @("commit", "-m", "Add rate limiting to login endpoint", "--feature", "auth", "--root", $ROOT)
$report += Run-Ctx "5.3 Create commit for database refactor" @("commit", "-m", "Refactor database connection pooling", "--feature", "database", "--root", $ROOT)

# ===== 6. Log commands =====
$report += "---"
$report += "# Part 6: Log & Show Commands"
$report += ""

$report += Run-Ctx "6.1 List all commits (ctx log)" @("log", "--root", $ROOT)
$report += Run-Ctx "6.2 List commits filtered by auth feature" @("log", "--feature", "auth", "--root", $ROOT)

# Get first commit ID from log output
$logOutput = & $CTX log --root $ROOT 2>&1 | Out-String
$commitIdMatch = [regex]::Match($logOutput, '([0-9a-f]{8})')
if ($commitIdMatch.Success) {
    $shortId = $commitIdMatch.Value
    $report += "## 6.3 Show first commit detail"
    $report += ""
    $report += "(Using short ID: $shortId)"
    $report += ""
    # We need the full commit ID - let's find it from .ctx/commits/
    $commitFiles = Get-ChildItem -Name (Join-Path $ROOT ".ctx\commits") -Filter "*.json" -ErrorAction SilentlyContinue
    if ($commitFiles) {
        $firstCommitFile = $commitFiles[0]
        $fullId = $firstCommitFile -replace '\.json$',''
        $report += Run-Ctx "Show commit $fullId" @("show", $fullId, "--root", $ROOT)
    }
}

# ===== 7. Link command =====
$report += "---"
$report += "# Part 7: Git Link"
$report += ""

# Use a fake git SHA for linking
$fakeSHA = "abc123def456789012345678901234567890abcd"
if ($commitFiles -and $commitFiles.Count -gt 0) {
    $fullId = ($commitFiles[0]) -replace '\.json$',''
    $report += Run-Ctx "7.1 Link git SHA to commit" @("link", $fakeSHA, "--commit", $fullId, "--root", $ROOT)
}

# ===== 8. Blame command =====
$report += "---"
$report += "# Part 8: Symbol Blame"
$report += ""

$report += Run-Ctx "8.1 Blame known symbol" @("blame", "auth.go#LoginHandler", "--root", $ROOT)
$report += Run-Ctx "8.2 Blame unknown symbol" @("blame", "unknown.go#NoFunction", "--root", $ROOT)

# ===== 9. Context command =====
$report += "---"
$report += "# Part 9: Context (Multi-Resolution View)"
$report += ""

$report += Run-Ctx "9.1 Context for feature 'auth'" @("context", "--feature", "auth", "--root", $ROOT)
$report += Run-Ctx "9.2 Context activity log" @("context", "--log", "10", "--root", $ROOT)
$report += Run-Ctx "9.3 Context metadata" @("context", "--metadata", "--root", $ROOT)

# ===== 10. Capture off =====
$report += "---"
$report += "# Part 10: Stop Capture"
$report += ""

# Kill the capture process
if ($captureJob -and -not $captureJob.HasExited) {
    Stop-Process -Id $captureJob.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 1
}
$report += Run-Ctx "10.1 Capture off" @("capture", "off", "--root", $ROOT)
$report += Run-Ctx "10.2 Capture status (after off)" @("capture", "status", "--root", $ROOT)

# ===== 11. Init idempotency =====
$report += "---"
$report += "# Part 11: Idempotency Test"
$report += ""
$report += Run-Ctx "11.1 Re-run ctx init (should not error)" @("init", "--root", $ROOT)

# ===== Summary =====
$report += "---"
$report += "# Summary"
$report += ""
$report += "| Feature | Status |"
$report += "|---------|--------|"
$report += "| ctx init | Tested |"
$report += "| ctx capture on | Tested |"
$report += "| ctx capture status | Tested |"
$report += "| ctx capture pause | Tested |"
$report += "| ctx capture resume | Tested |"
$report += "| ctx capture off | Tested |"
$report += "| HTTP event ingestion | Tested |"
$report += "| Invalid JSON rejection | Tested |"
$report += "| Secret redaction | Tested |"
$report += "| JSONL session files | Tested |"
$report += "| ctx session ls | Tested |"
$report += "| ctx session show | Tested |"
$report += "| ctx session show --turns | Tested |"
$report += "| ctx commit -m | Tested |"
$report += "| ctx log | Tested |"
$report += "| ctx log --feature | Tested |"
$report += "| ctx show <id> | Tested |"
$report += "| ctx link <sha> | Tested |"
$report += "| ctx blame <file#symbol> | Tested |"
$report += "| ctx context --feature | Tested |"
$report += "| ctx context --log | Tested |"
$report += "| ctx context --metadata | Tested |"
$report += "| Init idempotency | Tested |"
$report += ""
$report += "**Note:** MCP server (ctx mcp) tested via unit tests only (requires stdio transport)."
$report += "**Note:** Push/Pull tested via unit tests only (requires server running with PostgreSQL)."

# Write report
$report -join "`n" | Set-Content -Path $REPORT -Encoding UTF8
Write-Output "E2E report written to: $REPORT"
Write-Output "Total lines: $($report.Count)"
