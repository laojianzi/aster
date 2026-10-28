"""Normalize only the reviewed #121 PNG encoder edit for old scope hashes.

This is not a file exemption or a new baseline. The exact inverse edit must
recover every byte of the accepted file before its old inventory is hashed.
Use this only in the three historical path/mode/content scope guards; current
source provenance continues to hash the actual changed file without rewriting.
"""
import hashlib

SCREENSHOT_TEST = 'internal/ui/workbench_test.go'
FROZEN_SCREENSHOT_SHA256 = '28d6522f940aa57023d9a84837dbff32237ba44dea408f84739b0344f3ea7b73'
OLD_ENCODE = b'\tif err = png.Encode(f, tt.Image()); err != nil {\n'
FAST_ENCODE = (b'\tencoder := png.Encoder{CompressionLevel: png.BestSpeed}\n'
               b'\tif err = encoder.Encode(f, tt.Image()); err != nil {\n')


def restore_screenshot_encoder(data):
    if (not isinstance(data, bytes) or data.count(FAST_ENCODE) != 1
            or OLD_ENCODE in data):
        raise ValueError('unexpected native screenshot encoder edit')
    restored = data.replace(FAST_ENCODE, OLD_ENCODE, 1)
    if hashlib.sha256(restored).hexdigest() != FROZEN_SCREENSHOT_SHA256:
        raise ValueError('unexpected native screenshot helper content')
    return restored
