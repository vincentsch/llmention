package notify

import "os/exec"

// On Windows, os/exec kills only the script itself when it runs too long.
// Processes the script started keep running. WaitDelay still stops llmention
// from waiting for their output.
func configureCommandCancellation(cmd *exec.Cmd) {}
