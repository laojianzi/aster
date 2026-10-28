"""Invert only the reviewed PNG encoder edits for accepted scope hashes.

This is not a file exemption or a new baseline. The exact inverse edit must
recover every byte of the accepted file before its old inventory is hashed.
The native install scope restores #123's exact NoCompression postimage to the
accepted #121 BestSpeed file. Three older scopes then restore the original
png.Encode file. Source provenance always hashes the actual changed file.
"""
import hashlib

SCREENSHOT_TEST = 'internal/ui/workbench_test.go'
FROZEN_SCREENSHOT_SHA256 = '28d6522f940aa57023d9a84837dbff32237ba44dea408f84739b0344f3ea7b73'
ACCEPTED_FAST_SCREENSHOT_SHA256 = 'c9f5360054c9f456aa0c51d4e640e02810e1698d7528af09e239aa1b45797a38'
UNCOMPRESSED_SCREENSHOT_SHA256 = 'e5c0f77aedf6e8407d58223b6e9b4ee56ff261f4f7d6cabba560fdc9feabb0e2'
OLD_ENCODE = b'\tif err = png.Encode(f, tt.Image()); err != nil {\n'
FAST_ENCODE = (b'\tencoder := png.Encoder{CompressionLevel: png.BestSpeed}\n'
               b'\tif err = encoder.Encode(f, tt.Image()); err != nil {\n')
UNCOMPRESSED_ENCODE = (b'\tencoder := png.Encoder{CompressionLevel: png.NoCompression}\n'
                       b'\tif err = encoder.Encode(f, tt.Image()); err != nil {\n')


def restore_screenshot_best_speed(data):
    # Require the current exact postimage; accepting the old BestSpeed file as
    # an alternate would silently undo the newly admitted encoding decision.
    if (not isinstance(data, bytes) or data.count(UNCOMPRESSED_ENCODE) != 1
            or FAST_ENCODE in data or OLD_ENCODE in data):
        raise ValueError('unexpected uncompressed native screenshot encoder edit')
    if hashlib.sha256(data).hexdigest() != UNCOMPRESSED_SCREENSHOT_SHA256:
        raise ValueError('unexpected uncompressed native screenshot helper content')
    restored = data.replace(UNCOMPRESSED_ENCODE, FAST_ENCODE, 1)
    if hashlib.sha256(restored).hexdigest() != ACCEPTED_FAST_SCREENSHOT_SHA256:
        raise ValueError('unexpected accepted BestSpeed screenshot helper content')
    return restored


def restore_screenshot_encoder(data):
    data = restore_screenshot_best_speed(data)
    if (not isinstance(data, bytes) or data.count(FAST_ENCODE) != 1
            or OLD_ENCODE in data):
        raise ValueError('unexpected native screenshot encoder edit')
    restored = data.replace(FAST_ENCODE, OLD_ENCODE, 1)
    if hashlib.sha256(restored).hexdigest() != FROZEN_SCREENSHOT_SHA256:
        raise ValueError('unexpected native screenshot helper content')
    return restored
