import unittest
import os
import re
import tempfile
from pathlib import Path
from typing import Any
from unittest.mock import patch

import gate

SHA = "a" * 40
CI_WORKFLOW = Path(__file__).resolve().parents[3] / ".github" / "workflows" / "ci.yml"


def ci_job_bodies(workflow: str) -> dict[str, str]:
    """Split the workflow by job id, by indentation: the runners have no YAML library."""
    jobs_section = workflow.split("\njobs:\n", 1)[1]
    parts = re.split(r"(?m)^  ([A-Za-z_][\w-]*):[ ]*$", jobs_section)
    return dict(zip(parts[1::2], parts[2::2]))


def check_names(job_id: str, body: str) -> set[str]:
    """Return the names GitHub reports for this job, which are what the gate compares."""
    name = re.search(r"(?m)^    name: (.+)$", body)
    if name is None:
        if re.search(r"(?m)^ +matrix:", body):
            raise ValueError(f"Matrix job {job_id} needs an explicit name")
        return {job_id}
    if "${{" not in name[1]:
        return {name[1]}
    labels = re.findall(r"(?m)^ +- label: (\S+)$", body)
    return {name[1].replace("${{ matrix.label }}", label) for label in labels}


class GateTests(unittest.TestCase):
    def setUp(self) -> None:
        self.ci_run = {
            "head_sha": SHA,
            "head_branch": "main",
            "event": "push",
            "path": ".github/workflows/ci.yml",
            "head_repository": {"full_name": "BipoXDXD/stillon"},
            "status": "completed",
            "conclusion": "success",
        }
        self.jobs = [
            {"name": name, "status": "completed", "conclusion": "success"}
            for name in gate.REQUIRED["stillon"]
        ]

    def test_all_exact_jobs_success(self) -> None:
        self.assertEqual(gate.validate(self.ci_run, self.jobs, "stillon", SHA), SHA)

    def test_wrong_run_ref_repository_or_state_refused(self) -> None:
        for field, value in [
            ("head_sha", "b" * 40),
            ("head_branch", "feature"),
            ("event", "pull_request"),
            ("path", ".github/workflows/fake.yml"),
            ("conclusion", "failure"),
            ("head_repository", {"full_name": "fork/stillon"}),
            ("status", "in_progress"),
        ]:
            with self.subTest(field=field), self.assertRaises(ValueError):
                gate.validate({**self.ci_run, field: value}, self.jobs, "stillon", SHA)

    def test_missing_duplicate_skipped_failed_or_running_gate_refused(self) -> None:
        for jobs in [
            self.jobs[:-1],
            [*self.jobs, self.jobs[0]],
            [{**self.jobs[0], "conclusion": "skipped"}, *self.jobs[1:]],
            [{**self.jobs[0], "conclusion": "failure"}, *self.jobs[1:]],
            [{**self.jobs[0], "status": "in_progress"}, *self.jobs[1:]],
        ]:
            with self.assertRaises(ValueError):
                gate.validate(self.ci_run, jobs, "stillon", SHA)

    def test_sha_not_interpreted_as_arguments_or_newline(self) -> None:
        for sha in ["a" * 7, "--help", "a" * 40 + "\n"]:
            with self.assertRaises(ValueError):
                gate.validate(self.ci_run, self.jobs, "stillon", sha)


class RequiredMatchesCiWorkflowTests(unittest.TestCase):
    def setUp(self) -> None:
        self.bodies = ci_job_bodies(CI_WORKFLOW.read_text(encoding="utf-8"))
        self.ci_checks = {
            name for job_id, body in self.bodies.items() for name in check_names(job_id, body)
        }

    def test_every_required_gate_is_a_real_ci_check(self) -> None:
        self.assertEqual(gate.REQUIRED["stillon"] - self.ci_checks, set())

    def test_every_ci_check_is_a_required_gate(self) -> None:
        self.assertEqual(self.ci_checks - gate.REQUIRED["stillon"], set())

    def test_publish_waits_for_every_other_job(self) -> None:
        needs = re.search(r"(?m)^    needs: \[(.+)\]$", self.bodies["publish"])
        self.assertIsNotNone(needs)
        self.assertEqual(
            {job_id.strip() for job_id in needs[1].split(",")},
            set(self.bodies) - {"publish"},
        )


class SupersededReleaseTests(unittest.TestCase):
    def test_superseded_release_is_successful_noop_before_any_ci_query(self):
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'output'
            env = {'RELEASE_PROJECT': 'stillon', 'GITHUB_REPOSITORY': 'BipoXDXD/stillon',
                   'RELEASE_COMMIT': SHA, 'GITHUB_OUTPUT': str(output),
                   'SKIP_SUPERSEDED': 'true'}
            with patch.dict(os.environ, env, clear=True), patch.object(
                    gate, 'api', return_value={'sha': 'b' * 40}) as api:
                gate.main()
            self.assertIn('eligible=false', output.read_text())
            self.assertEqual(api.call_count, 1)

    def test_strict_callers_still_refuse_stale_commits(self):
        with patch.dict(os.environ, {'RELEASE_PROJECT': 'stillon',
                'GITHUB_REPOSITORY': 'BipoXDXD/stillon', 'RELEASE_COMMIT': SHA}, clear=True), \
                patch.object(gate, 'api', return_value={'sha': 'b' * 40}):
            with self.assertRaises(ValueError):
                gate.main()


class ManualClock:
    def __init__(self) -> None:
        self.now = 0.0
        self.slept: list[float] = []

    def monotonic(self) -> float:
        return self.now

    def sleep(self, seconds: float) -> None:
        self.slept.append(seconds)
        self.now += seconds


class GateMainTests(unittest.TestCase):
    """Drive main() through its last lines with GitHub replaced path by path."""

    REPO = "repos/BipoXDXD/stillon/"
    MAIN = REPO + "commits/main"
    RUN = REPO + "actions/runs/4242"
    JOBS = RUN + "/attempts/2/jobs?per_page=100"
    RUNS = (
        REPO + "actions/workflows/ci.yml/runs?event=push&branch=main&head_sha=" + SHA + "&per_page=100"
    )
    NEWER_MAIN = {"sha": "b" * 40}
    SAME_MAIN = {"sha": SHA}
    ELIGIBLE = f"eligible=true\ncommit={SHA}\nci_run=4242\nci_attempt=2\n"

    def setUp(self) -> None:
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.output = Path(temp.name) / "output"
        self.clock = ManualClock()
        self.ci_run = {
            "id": 4242,
            "run_attempt": 2,
            "head_sha": SHA,
            "head_branch": "main",
            "event": "push",
            "path": ".github/workflows/ci.yml",
            "head_repository": {"full_name": "BipoXDXD/stillon"},
            "status": "completed",
            "conclusion": "success",
        }
        self.jobs = [
            {"name": name, "status": "completed", "conclusion": "success"}
            for name in sorted(gate.REQUIRED["stillon"])
        ]

    def job_page(self, jobs: list[dict[str, str]], total_count: int | None = None) -> dict[str, Any]:
        return {"total_count": len(jobs) if total_count is None else total_count, "jobs": jobs}

    def run_main(self, answers: dict[str, list[Any]], **environment: str) -> list[str]:
        """Run main() and return the GitHub paths it asked for, in order."""
        pending = {path: list(replies) for path, replies in answers.items()}
        asked: list[str] = []

        def api(path: str) -> Any:
            asked.append(path)
            if not pending.get(path):
                raise AssertionError("Unexpected GitHub request: " + path)
            return pending[path].pop(0)

        env = {
            "RELEASE_PROJECT": "stillon",
            "GITHUB_REPOSITORY": "BipoXDXD/stillon",
            "RELEASE_COMMIT": SHA,
            "GITHUB_OUTPUT": str(self.output),
            **environment,
        }
        with (
            patch.dict(os.environ, env, clear=True),
            patch.object(gate, "api", side_effect=api),
            patch("time.monotonic", self.clock.monotonic),
            patch("time.sleep", self.clock.sleep),
        ):
            gate.main()
        return asked

    def answers(
        self, run: dict[str, Any] | None = None, page: dict[str, Any] | None = None
    ) -> dict[str, list[Any]]:
        return {
            self.MAIN: [self.SAME_MAIN, self.SAME_MAIN],
            self.RUN: [self.ci_run if run is None else run],
            self.JOBS: [self.job_page(self.jobs) if page is None else page],
        }

    def assert_refused_without_output(
        self, message: str, answers: dict[str, list[Any]], **environment: str
    ) -> None:
        with self.assertRaisesRegex(ValueError, message):
            self.run_main(answers, **environment)
        self.assertFalse(self.output.exists())

    def test_successful_ci_records_exact_release_and_rechecks_main(self) -> None:
        asked = self.run_main(self.answers(), CI_RUN_ID="4242")

        self.assertEqual(self.output.read_text(), self.ELIGIBLE)
        self.assertEqual(asked, [self.MAIN, self.RUN, self.JOBS, self.MAIN])
        self.assertEqual(self.clock.slept, [])

    def test_red_or_incomplete_jobs_are_not_eligible(self) -> None:
        failed = [{**self.jobs[0], "conclusion": "failure"}, *self.jobs[1:]]
        cancelled = [{**self.jobs[0], "conclusion": "cancelled"}, *self.jobs[1:]]
        for name, jobs, message in [
            ("failed", failed, "Required gate did not succeed"),
            ("cancelled", cancelled, "Required gate did not succeed"),
            ("missing", self.jobs[:-1], "Missing, extra or duplicated CI gate"),
            ("empty", [], "Missing, extra or duplicated CI gate"),
        ]:
            with self.subTest(name=name):
                self.assert_refused_without_output(
                    message, self.answers(page=self.job_page(jobs)), CI_RUN_ID="4242"
                )

    def test_unsuccessful_or_foreign_run_is_not_eligible(self) -> None:
        for field, value in [
            ("conclusion", "failure"),
            ("head_sha", "c" * 40),
            ("head_branch", "feature"),
        ]:
            with self.subTest(field=field):
                self.assert_refused_without_output(
                    "Only successful CI main push",
                    self.answers(run={**self.ci_run, field: value}),
                    CI_RUN_ID="4242",
                )

    def test_truncated_job_page_is_not_eligible(self) -> None:
        page = self.job_page(self.jobs, total_count=len(self.jobs) + 1)

        self.assert_refused_without_output(
            "Incomplete CI gate response", self.answers(page=page), CI_RUN_ID="4242"
        )

    def test_main_moving_during_the_gate_refuses_strict_callers(self) -> None:
        answers = {**self.answers(), self.MAIN: [self.SAME_MAIN, self.NEWER_MAIN]}

        self.assert_refused_without_output("Superseded main commit", answers, CI_RUN_ID="4242")

    def test_main_moving_during_the_gate_is_a_noop_when_superseding_is_allowed(self) -> None:
        answers = {**self.answers(), self.MAIN: [self.SAME_MAIN, self.NEWER_MAIN]}

        asked = self.run_main(answers, CI_RUN_ID="4242", SKIP_SUPERSEDED="true")

        self.assertEqual(self.output.read_text(), "eligible=false\n")
        self.assertEqual(asked, [self.MAIN, self.RUN, self.JOBS, self.MAIN])

    def test_invalid_run_id_is_refused_before_any_ci_query(self) -> None:
        answers = {self.MAIN: [self.SAME_MAIN]}

        self.assert_refused_without_output("Invalid CI run id", answers, CI_RUN_ID="4242/../1")

    def test_without_run_id_the_newest_run_of_the_commit_decides(self) -> None:
        older = {**self.ci_run, "id": 4000, "run_attempt": 1, "conclusion": "failure"}
        oldest = {**older, "id": 3000}
        answers = {
            self.MAIN: [self.SAME_MAIN, self.SAME_MAIN],
            self.RUNS: [{"workflow_runs": [older, self.ci_run, oldest]}],
            self.JOBS: [self.job_page(self.jobs)],
        }

        asked = self.run_main(answers)

        self.assertEqual(self.output.read_text(), self.ELIGIBLE)
        self.assertEqual(asked, [self.MAIN, self.RUNS, self.JOBS, self.MAIN])

    def test_waiting_gate_polls_until_ci_finishes(self) -> None:
        running = {**self.ci_run, "status": "in_progress", "conclusion": None}
        answers = {
            self.MAIN: [self.SAME_MAIN, self.SAME_MAIN],
            self.RUNS: [{"workflow_runs": [running]}, {"workflow_runs": [self.ci_run]}],
            self.JOBS: [self.job_page(self.jobs)],
        }

        self.run_main(answers, WAIT_FOR_CI="true")

        self.assertEqual(self.output.read_text(), self.ELIGIBLE)
        self.assertEqual(self.clock.slept, [10])

    def test_waiting_gate_gives_up_after_thirty_minutes(self) -> None:
        running = {**self.ci_run, "status": "in_progress", "conclusion": None}
        answers = {
            self.MAIN: [self.SAME_MAIN],
            self.RUNS: [{"workflow_runs": [running]}] * 181,
        }

        self.assert_refused_without_output(
            "CI did not finish before release timeout", answers, WAIT_FOR_CI="true"
        )
        self.assertEqual(self.clock.slept, [10] * 180)

    def test_without_waiting_a_commit_with_no_run_is_refused_at_once(self) -> None:
        answers = {self.MAIN: [self.SAME_MAIN], self.RUNS: [{"workflow_runs": []}]}

        self.assert_refused_without_output("CI did not finish before release timeout", answers)
        self.assertEqual(self.clock.slept, [])


if __name__ == "__main__":
    unittest.main()
