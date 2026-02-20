import subprocess
import json
import time

msg = {
    "type": "message",
    "role": "user",
    "content": [{"type": "text", "text": "hello world"}]
}

cmd = [
    "claude", "-p",
    "--input-format", "stream-json",
    "--output-format", "stream-json",
    "--verbose",
    "--include-partial-messages",
    "Just say hi back"
]

proc = subprocess.Popen(
    cmd,
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
    bufsize=1  # Line-buffered for realtime output
)

# Send message and immediately close stdin to signal end
proc.stdin.write(json.dumps(msg) + "\n")
proc.stdin.flush()
proc.stdin.close()

# Read output with timeout (kill if no progress after 30s)
start_time = time.time()
stdout_lines = []
stderr_lines = []

while True:
    if time.time() - start_time > 30:
        print("Timeout: Killing process due to hang")
        proc.kill()
        break

    line = proc.stdout.readline()
    if line:
        print("STDOUT:", line.strip())
        stdout_lines.append(line)
    err_line = proc.stderr.readline()
    if err_line:
        print("STDERR:", err_line.strip())
        stderr_lines.append(err_line)

    if not line and proc.poll() is not None:
        break

    time.sleep(0.1)  # Light polling to avoid CPU spin

# Final cleanup
proc.wait()
print("Exit code:", proc.returncode)
if not stdout_lines:
    print("No stdout produced — likely CLI hang. Check if --input-format stream-json works in interactive mode.")