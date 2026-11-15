import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import shutil
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
import urllib.error

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from adapter import HTTPTransport


@unittest.skipUnless(shutil.which("openssl"), "Local TLS fixture generation requires openssl")
class HTTPSTransportTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory()
        cls.root = Path(cls.temp.name)
        config = cls.root / "openssl.cnf"
        config.write_text("[req]\ndistinguished_name=dn\nx509_extensions=extensions\nprompt=no\n[dn]\nCN=localhost\n[extensions]\nsubjectAltName=DNS:localhost\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,digitalSignature,keyEncipherment,keyCertSign\n")
        for name in ("server", "unrelated"):
            subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "2",
                            "-config", str(config), "-keyout", str(cls.root / (name + ".key")), "-out", str(cls.root / (name + ".pem"))],
                           check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        cls.cert = cls.root / "server.pem"
        cls.pin = hashlib.sha256(ssl.PEM_cert_to_DER_cert(cls.cert.read_text())).hexdigest()
        cls.token_file = cls.root / "test-token"
        cls.token_file.write_text("synthetic-ingest-test-token")
        cls.requests = []
        requests = cls.requests
        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                payload = self.rfile.read(int(self.headers["Content-Length"]))
                requests.append({"path": self.path, "authorization": self.headers.get("Authorization"), "payload": json.loads(payload)})
                if self.path == "/redirect":
                    self.send_response(302)
                    self.send_header("Location", "/unexpected-redirect-target")
                    self.end_headers()
                    return
                body = b'{"accepted":1,"duplicates":0}'
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *_):
                pass
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(str(cls.cert), str(cls.root / "server.key"))
        cls.server.socket = context.wrap_socket(cls.server.socket, server_side=True)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.port = cls.server.server_port

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()
        cls.temp.cleanup()

    def setUp(self):
        self.requests.clear()

    def transport(self, hostname="127.0.0.1", **kwargs):
        return HTTPTransport(f"https://{hostname}:{self.port}", self.token_file, timeout=3, **kwargs)

    def test_custom_ca_and_matching_hostname_send_authorized_request(self):
        result = self.transport(hostname="localhost", ca_file=str(self.cert)).post("/events", {"events": []})
        self.assertEqual(result, {"accepted": 1, "duplicates": 0})
        self.assertEqual(self.requests[0]["authorization"], "Bearer synthetic-ingest-test-token")

    def test_ca_does_not_ignore_missing_ip_san(self):
        with self.assertRaises(urllib.error.URLError):
            self.transport(ca_file=str(self.cert)).post("/events", {"events": []})
        self.assertEqual(self.requests, [])

    def test_pin_only_accepts_exact_self_signed_leaf_with_mismatching_ip_san(self):
        self.transport(cert_sha256=self.pin.upper()).post("/events", {"events": []})
        self.assertEqual(len(self.requests), 1)
        self.assertEqual(self.requests[0]["authorization"], "Bearer synthetic-ingest-test-token")

    def test_wrong_pin_rejects_before_http_headers_or_body(self):
        with self.assertRaises(urllib.error.URLError):
            self.transport(cert_sha256="0" * 64).post("/events", {"events": []})
        self.assertEqual(self.requests, [])

    def test_ca_plus_pin_preserves_chain_verification_and_uses_pin_identity(self):
        self.transport(ca_file=str(self.cert), cert_sha256=self.pin).post("/events", {"events": []})
        self.assertEqual(len(self.requests), 1)
        self.requests.clear()
        with self.assertRaises(urllib.error.URLError):
            self.transport(ca_file=str(self.root / "unrelated.pem"), cert_sha256=self.pin).post("/events", {"events": []})
        self.assertEqual(self.requests, [])

    def test_default_system_ca_rejects_unknown_self_signed_certificate(self):
        with self.assertRaises(urllib.error.URLError):
            self.transport().post("/events", {"events": []})
        self.assertEqual(self.requests, [])

    def test_pinned_connection_still_rejects_redirects(self):
        with self.assertRaises(urllib.error.HTTPError) as error:
            self.transport(cert_sha256=self.pin).post("/redirect", {"events": []})
        self.assertEqual(error.exception.code, 302)
        self.assertEqual([request["path"] for request in self.requests], ["/redirect"])

    def test_tls_configuration_rejects_malformed_pin_and_plain_http(self):
        for pin in ("", "sha256:" + self.pin, "z" * 64, self.pin + "\n", "1" * 63):
            if not pin:  # Empty optional environment value means ordinary CA validation.
                continue
            with self.assertRaises(ValueError):
                self.transport(cert_sha256=pin)
        for kwargs in ({"cert_sha256": self.pin}, {"ca_file": str(self.cert)}):
            with self.assertRaisesRegex(ValueError, "requires_https"):
                HTTPTransport("http://localhost", self.token_file, **kwargs)


if __name__ == "__main__":
    unittest.main()
