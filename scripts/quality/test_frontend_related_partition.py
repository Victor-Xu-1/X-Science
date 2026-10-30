"""Focused contract tests for complete, deterministic related-test shards."""
import copy
import unittest

from scripts.quality import frontend_related_partition as subject


class FrontendRelatedPartitionTests(unittest.TestCase):
    def setUp(self):
        self.binding = {'head': 'a' * 40, 'base': 'b' * 40, 'inputs_sha256': 'c' * 64}
        self.items = [
            {'project': 'dom', 'path': 'tests/shared.test.ts', 'pool': 'forks'},
            {'project': 'node', 'path': 'tests/shared.test.ts', 'pool': 'forks'},
            {'project': 'node', 'path': 'tests/z.test.ts', 'pool': 'forks'},
        ]

    def passing(self, plan):
        return [
            {'index': part['index'], 'binding': self.binding, 'exit_code': 0, 'reason': 'passed',
             'unhandled_errors': 0, 'queued': part['specs'],
             'ended': [{**spec, 'state': 'passed', 'ok': True} for spec in part['specs']]}
            for part in plan['shards']
        ]

    def test_stable_triplet_partition_with_same_path_different_projects(self):
        a = subject.plan(self.binding, self.items, max_shards=2)
        b = subject.plan(self.binding, list(reversed(self.items)), max_shards=2)
        self.assertEqual(a, b)
        self.assertEqual([len(x['specs']) for x in a['shards']], [2, 1])
        self.assertTrue(subject.verify(a, self.binding, self.passing(a)))

    def test_tiny_and_empty_inventory_keep_existing_semantics(self):
        small = subject.plan(self.binding, self.items[:1], max_shards=4)
        self.assertEqual(len(small['shards']), 1)
        self.assertTrue(subject.verify(small, self.binding, self.passing(small)))
        empty = subject.plan(self.binding, [], max_shards=4)
        self.assertEqual(empty['shards'], [])
        self.assertTrue(subject.verify(empty, self.binding, []))

    def test_skipped_module_is_terminal_not_an_invented_failure(self):
        plan = subject.plan(self.binding, self.items, max_shards=2)
        reports = self.passing(plan)
        reports[0]['ended'][0]['state'] = 'skipped'
        self.assertTrue(subject.verify(plan, self.binding, reports))

    def test_missing_duplicate_or_incorrect_partition_fails(self):
        plan = subject.plan(self.binding, self.items, max_shards=2)
        reports = self.passing(plan)
        cases = [reports[:1], [reports[0], reports[0]]]
        wrong = copy.deepcopy(reports)
        wrong[0]['queued'][0]['project'] = 'wrong'
        cases.append(wrong)
        for case in cases:
            with self.subTest(case=case), self.assertRaises(ValueError):
                subject.verify(plan, self.binding, case)

    def test_failure_cancellation_unhandled_or_unfinished_fails(self):
        plan = subject.plan(self.binding, self.items, max_shards=2)
        for field, value in [('exit_code', 1), ('reason', 'interrupted'), ('unhandled_errors', 1)]:
            reports = self.passing(plan)
            reports[0][field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                subject.verify(plan, self.binding, reports)
        for state, ok in [('failed', False), ('pending', True)]:
            reports = self.passing(plan)
            reports[0]['ended'][0].update(state=state, ok=ok)
            with self.subTest(state=state), self.assertRaises(ValueError):
                subject.verify(plan, self.binding, reports)

    def test_wrong_identity_missing_events_and_invalid_inputs_fail_closed(self):
        plan = subject.plan(self.binding, self.items, max_shards=2)
        reports = self.passing(plan)
        reports[0]['binding'] = {**self.binding, 'head': 'd' * 40}
        with self.assertRaises(ValueError):
            subject.verify(plan, self.binding, reports)
        reports = self.passing(plan)
        reports[0]['ended'].pop()
        with self.assertRaises(ValueError):
            subject.verify(plan, self.binding, reports)
        for item in [self.items[0], {'project': 'node', 'path': '../outside', 'pool': 'forks'}]:
            with self.assertRaises(ValueError):
                subject.plan(self.binding, [self.items[0], item], max_shards=2)
        with self.assertRaises(ValueError):
            subject.plan({**self.binding, 'head': 'a' * 41}, self.items, max_shards=2)

    def test_native_report_must_reproduce_the_complete_selection_on_each_worker(self):
        plan = subject.plan(self.binding, self.items, max_shards=2)
        native = []
        for report in self.passing(plan):
            native.append({'index': report['index'], 'binding': report['binding'], 'exit_code': 0,
                           'report': {'planned': list(reversed(plan['specs'])), 'queued': report['queued'],
                                      'ended': report['ended'], 'reason': 'passed', 'unhandled_errors': 0}})
        self.assertTrue(subject.verify_native(plan, self.binding, native))
        changed = copy.deepcopy(native)
        changed[0]['report']['planned'].pop()
        with self.assertRaises(ValueError):
            subject.verify_native(plan, self.binding, changed)
        changed = copy.deepcopy(native)
        changed[0]['report']['planned'][0]['pool'] = 'threads'
        with self.assertRaises(ValueError):
            subject.verify_native(plan, self.binding, changed)


if __name__ == '__main__':
    unittest.main()
