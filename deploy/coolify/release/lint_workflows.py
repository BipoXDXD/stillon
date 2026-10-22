"""Validate the documented queue policy, then lint all workflow content with actionlint.

Actionlint 1.7.12 predates GitHub's queue:max. This adapter recognizes only the
reviewed production/PR policy in coolify.yml; it never ignores parser diagnostics.
https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency
"""
import re
import subprocess
from pathlib import Path

GROUP = "${{ github.event_name == 'pull_request' && format('coolify-pr-{0}', github.event.pull_request.number) || 'coolify-production' }}"
POLICY = re.compile(
    r'(?m)^(concurrency:\n(?:  #[^\n]*\n)*  group: ' + re.escape(GROUP)
    + r'\n  cancel-in-progress: false\n)  queue: max\n'
)


def normalize_queue(source):
    # A blank line preserves every downstream diagnostic's original line number.
    normalized, count = POLICY.subn(lambda match: match.group(1) + '\n', source)
    if count != 1:
        raise ValueError('Exactly one reviewed Coolify queue:max policy is required')
    return normalized


def main():
    target = Path('.github/workflows/coolify.yml')
    normalized = normalize_queue(target.read_text())
    other = sorted(str(p) for p in target.parent.iterdir()
                   if p.is_file() and p.suffix in {'.yml', '.yaml'} and p != target)
    if other:
        subprocess.run(['actionlint', *other], check=True)
    subprocess.run(['actionlint', '-stdin-filename', str(target), '-'],
                   input=normalized.encode(), check=True)


if __name__ == '__main__':
    main()
