"""Real Pillow display encoding; the provider boundary is a local stub only."""
import base64
import importlib.util
import io
from pathlib import Path
import sys
import tempfile
import unittest

from PIL import Image


ROOT = Path(__file__).resolve().parents[2]
BRIDGE = ROOT / 'assets/optional/kernels/x_science_host_bridge.py'


class HostBridgeImagePresentationTests(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location('image_presentation_bridge', BRIDGE)
        self.bridge = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.bridge)
        self.calls = []

        def provider(method, args, kwargs):
            self.assertEqual(method, 'host.llm_batch')
            self.assertEqual(len(args[0]), 1)
            self.assertEqual(args[1], 1)
            self.calls.append(args[0][0])
            return [{'text': 'image fixture response'}]

        self.bridge._host_rpc = provider
        self.bridge._install_host_module()
        self.host = sys.modules['host']

    def tearDown(self):
        self.bridge._remove_host_module()

    def present(self, image):
        request = {'messages': [{'role': 'user', 'content': [
            {'type': 'image', 'pil': image, 'caption': 'fixture'},
            {'type': 'text', 'text': 'Inspect this presentation.'},
        ]}]}
        result = self.host.llm(request)
        self.assertEqual(result['text'], 'image fixture response')
        self.assertIs(request['messages'][0]['content'][0]['pil'], image)
        block = self.calls[-1]['messages'][0]['content'][0]
        self.assertEqual(block['caption'], 'fixture')
        self.assertNotIn('pil', block)
        self.assertEqual(block['source']['media_type'], 'image/png')
        decoded = Image.open(io.BytesIO(base64.b64decode(block['source']['data'], validate=True)))
        decoded.load()
        return decoded

    def test_integer_pixels_are_presented_without_mutating_original(self):
        with tempfile.TemporaryDirectory(prefix='pillow-presentation-') as directory:
            original = Path(directory) / 'integer-original.tiff'
            image = Image.new('I', (2, 2))
            image.putdata([-5, 100, 65536, 1000000])
            image.save(original)
            file_before = original.read_bytes()
            pixels_before = image.tobytes()
            expected = image.convert('RGB')
            presented = self.present(image)
            self.assertEqual(presented.mode, 'RGB')
            self.assertEqual(presented.tobytes(), expected.tobytes())
            self.assertEqual(image.mode, 'I')
            self.assertEqual(image.tobytes(), pixels_before)
            self.assertEqual(original.read_bytes(), file_before)
            self.assertEqual(len(self.calls), 1)

    def test_png_alpha_modes_keep_llm_transparency(self):
        gray_alpha = Image.new('LA', (2, 2), (90, 77))
        palette_alpha = Image.new('P', (2, 2))
        palette_alpha.putpalette([10, 20, 30, 40, 50, 60] + [0] * (256 * 3 - 6))
        palette_alpha.putdata([0, 1, 1, 0])
        palette_alpha.info['transparency'] = bytes([0, 113] + [255] * 254)
        for image in [gray_alpha, palette_alpha]:
            with self.subTest(mode=image.mode):
                pixels_before = image.tobytes()
                palette_before = image.getpalette()
                metadata_before = dict(image.info)
                expected = image.convert('RGBA').tobytes()
                presented = self.present(image)
                self.assertEqual(presented.mode, image.mode)
                self.assertEqual(presented.convert('RGBA').tobytes(), expected)
                self.assertEqual(image.tobytes(), pixels_before)
                self.assertEqual(image.getpalette(), palette_before)
                self.assertEqual(image.info, metadata_before)

    def test_rgb_rgba_and_existing_view_image_preserve_display_pixels(self):
        for mode, color in [('RGB', (10, 20, 30)), ('RGBA', (10, 20, 30, 77))]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory(prefix='pillow-display-') as directory:
                image = Image.new(mode, (4, 4), color)
                pixels_before = image.tobytes()
                presented = self.present(image)
                self.assertEqual(presented.mode, mode)
                self.assertEqual(presented.tobytes(), pixels_before)
                output = Path(directory) / 'view.png'
                result = self.host.view_image(image, crop=(0.25, 0.25, 0.75, 0.75), max_size=None, out=str(output))
                self.assertEqual(result['output_size'], (2, 2))
                with Image.open(output) as viewed:
                    self.assertEqual(viewed.mode, mode)
                    self.assertEqual(viewed.getpixel((0, 0)), color)
                self.assertEqual(image.size, (4, 4))
                self.assertEqual(image.tobytes(), pixels_before)


if __name__ == '__main__':
    unittest.main()
