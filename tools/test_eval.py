import os, sys, unittest
sys.path.insert(0, os.path.dirname(__file__))
import eval as ev  # noqa: E402


class ChrF(unittest.TestCase):
    def test_identical(self):
        self.assertAlmostEqual(ev.chrf_simple("Olá mundo", "Olá mundo"), 100.0)

    def test_disjoint(self):
        self.assertEqual(ev.chrf_simple("aaaa", "bbbb"), 0.0)

    def test_order(self):
        close = ev.chrf_simple("Espada de ferro", "Espada de ferro grande")
        far = ev.chrf_simple("Maçã", "Espada de ferro grande")
        self.assertGreater(close, far)

    def test_spaces_ignored(self):
        self.assertAlmostEqual(ev.chrf_simple("a b c", "abc"), 100.0)


if __name__ == "__main__":
    unittest.main()
