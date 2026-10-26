import unittest
from types import SimpleNamespace

from vylux_manual.api import VyluxAPI
from vylux_manual.signing import create_image_source_token, sign_encrypted_image, sign_encrypted_original


class ImageSourceSigningTest(unittest.TestCase):
    def test_cross_language_vector(self) -> None:
        image_source_key = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
        hmac_secret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
        nonce = bytes.fromhex("000102030405060708090a0b")

        token = create_image_source_token("static/forum/recommend-cover.png", image_source_key, nonce)
        self.assertEqual(
            token,
            "AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg",
        )

        image_signature, options, output_format = sign_encrypted_image(
            hmac_secret,
            "q90_h278_w671",
            token,
            "avif",
        )
        self.assertEqual(options, "w671_h278_q90")
        self.assertEqual(output_format, "avif")
        self.assertEqual(image_signature, "a01588e17e4abaeb812697d3b41006dd90d60f44c287c918731ac9e27ffbcb55")
        self.assertEqual(
            sign_encrypted_original(hmac_secret, token),
            "ea4f2676019686704ca7bd20e5b3a801f6f218e4f4c4d2b7bde7978c482c0021",
        )

    def test_random_nonce_changes_token(self) -> None:
        key = "00" * 32
        first = create_image_source_token("uploads/image.png", key)
        second = create_image_source_token("uploads/image.png", key)
        self.assertNotEqual(first, second)

    def test_encrypted_delivery_urls(self) -> None:
        settings = SimpleNamespace(
            base_url="https://media.example.com",
            hmac_secret="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            image_source_key="00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
        )
        api = VyluxAPI(settings)  # type: ignore[arg-type]
        nonce = bytes.fromhex("000102030405060708090a0b")
        token = api.build_source_token(source_key="static/forum/recommend-cover.png", nonce=nonce)

        self.assertEqual(
            api.build_image_url(
                source_key="static/forum/recommend-cover.png",
                output_format="avif",
                options="q90_h278_w671",
                source_token=token,
            ),
            "https://media.example.com/img/a01588e17e4abaeb812697d3b41006dd90d60f44c287c918731ac9e27ffbcb55/w671_h278_q90/"
            "AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg.avif",
        )
        self.assertEqual(
            api.build_original_url(source_key="static/forum/recommend-cover.png", source_token=token),
            "https://media.example.com/original/ea4f2676019686704ca7bd20e5b3a801f6f218e4f4c4d2b7bde7978c482c0021/"
            "AQABAgMEBQYHCAkKC2Vs1EIpKuLQpkkakR6nAUwBWMoblWBW_FZUJL4KkkRQvPT1Ob-M_IsC7s3mG3OUcg",
        )

    def test_plaintext_delivery_url_without_image_source_key(self) -> None:
        settings = SimpleNamespace(
            base_url="https://media.example.com",
            hmac_secret="test-secret",
            image_source_key="",
        )
        api = VyluxAPI(settings)  # type: ignore[arg-type]

        url = api.build_image_url(source_key="uploads/sample image.png", output_format="webp", options="w320")
        self.assertIn("/img/", url)
        self.assertTrue(url.endswith("/uploads%2Fsample%20image.png.webp"))


if __name__ == "__main__":
    unittest.main()
