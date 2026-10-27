# Reporting a security issue

Report security issues through
[GitHub private vulnerability reporting](https://github.com/vincentsch/llmention/security/advisories/new).
Never post keys or private answers in a public issue. The same goes for
details of how to exploit a problem.

Include the llmention version and your operating system, and the steps that
show the problem. Use made-up keys and prompts in examples. Remove keys and
private content from anything you share, such as logs or screenshots.

## Where llmention handles private data

API keys come from environment variables. llmention never writes them to
disk and removes them from what it prints. A key sent to a `base_url` must
travel over https unless the host is the local machine.

Saved runs hold your prompts and the full answers. They stay in `runs_dir`
on your machine unless you copy them elsewhere. llmention reads run files
without following symlinks, so a link that someone put in the folder cannot
make it read another file.

An AI grader and alerts receive data from your runs.
The grader needs `allow_answer_disclosure: true` before it may read answers.
