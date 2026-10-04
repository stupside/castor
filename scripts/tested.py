"""Reprint `go test -json` as humans read it, and fail on a skip that hides coverage."""

import json
import sys

# A redirected stdout is block buffered, so a slow suite would look hung.
sys.stdout.reconfigure(line_buffering=True)

skipped = []
for line in sys.stdin:
    try:
        event = json.loads(line)
    except ValueError:
        sys.stdout.write(line)
        continue
    action = event.get("Action")
    # A compile failure reports its diagnostics only as build-output.
    if action in ("output", "build-output"):
        sys.stdout.write(event.get("Output", ""))
    elif action == "skip" and event.get("Test"):
        skipped.append(event["Package"] + "." + event["Test"])

if skipped:
    print("\nthese tests skipped, so the coverage they hold did not run:", file=sys.stderr)
    for name in skipped:
        print("  " + name, file=sys.stderr)
    print(
        "\nevery skip castor ships is gated on a media tool the test job installs,\n"
        "so this means the tool went missing and the coverage silently left with it.",
        file=sys.stderr,
    )
    sys.exit(1)
