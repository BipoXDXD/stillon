import subprocess
import unittest
from contextlib import chdir
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

import lint_workflows


class QueueLintTests(unittest.TestCase):
    def setUp(self):
        self.source = (Path(__file__).resolve().parents[3] / '.github/workflows/coolify.yml').read_text()

    def test_only_the_supported_queue_property_is_removed_for_legacy_parser(self):
        normalized = lint_workflows.normalize_queue(self.source)
        self.assertEqual(normalized, self.source.replace('  queue: max\n', '\n', 1))
        self.assertIn('cancel-in-progress: false', normalized)
        self.assertEqual(len(normalized.splitlines()), len(self.source.splitlines()))

    def test_unsupported_or_changed_queue_policy_is_refused(self):
        for source in [self.source.replace('queue: max', 'queue: unlimited'),
                       self.source.replace('cancel-in-progress: false', 'cancel-in-progress: true'),
                       self.source.replace("'coolify-production'", "'different-production'"),
                       self.source.replace('concurrency:', 'other:')]:
            with self.subTest(source=source), self.assertRaises(ValueError):
                lint_workflows.normalize_queue(source)

    def test_queue_in_other_text_is_not_silently_removed(self):
        extra = '\n# example\nexample: |\n  queue: max\n'
        self.assertTrue(lint_workflows.normalize_queue(self.source + extra).endswith(extra))

    def test_lints_repository_workflows_from_another_directory(self):
        root = Path(__file__).resolve().parents[3]
        with TemporaryDirectory() as directory, chdir(directory):
            with patch.object(lint_workflows.subprocess, 'run') as run:
                lint_workflows.main()

        other, coolify = run.call_args_list
        self.assertIn('.github/workflows/ci.yml', other.args[0])
        self.assertNotIn('.github/workflows/coolify.yml', other.args[0])
        self.assertEqual(other.kwargs['cwd'], root)
        self.assertEqual(coolify.args[0], ['actionlint', '-stdin-filename', '.github/workflows/coolify.yml', '-'])
        self.assertEqual(coolify.kwargs['cwd'], root)
        self.assertEqual(coolify.kwargs['input'], lint_workflows.normalize_queue(self.source).encode())

    def test_linter_errors_outside_supported_policy_are_propagated(self):
        broken = self.source.replace('RELEASE_PROJECT: stillon',
                                     'RELEASE_PROJECT: ${{ definitely_unknown_context.value }}')
        error = subprocess.CalledProcessError(1, ['actionlint'])
        for effects in [[error], [subprocess.CompletedProcess(['actionlint'], 0), error]]:
            with self.subTest(effects=effects), patch.object(Path, 'read_text', return_value=broken), \
                    patch.object(lint_workflows.subprocess, 'run', side_effect=effects) as run:
                with self.assertRaises(subprocess.CalledProcessError):
                    lint_workflows.main()
                if len(effects) == 2:
                    self.assertIn(b'definitely_unknown_context', run.call_args.kwargs['input'])
