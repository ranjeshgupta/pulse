# A pretend SMS company's API for the pictures of docs/tutorials/16-connecting-any-provider.md
# (shots/16-connecting-any-provider.mjs). It runs in a container on the compose network, never on
# the host network, so Pulse reaches it as http://sms-api:8080 (development servers allow private
# addresses through PULSE_ALLOW_PRIVATE_WEBHOOKS):
#
#   docker run -d --rm --name sms-api --network pulse_default -v "$PWD/docs/screenshots:/m:ro" \
#     python:3.12-alpine python /m/mock-provider.py
#   make docs-shots SHOT=16
#   docker stop sms-api
#
# POST /v1/login     {"username", "password"} -> {"data": {"access_token", "expires_in": 3600}}
# POST /v1/messages  Authorization: Bearer <token> -> {"status": "queued", "message_id": "..."}
import json
import uuid
from http.server import BaseHTTPRequestHandler, HTTPServer

TOKEN = "smsco-" + uuid.uuid4().hex


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except ValueError:
            body = {}
        if self.path == "/v1/login":
            if body.get("username") and body.get("password"):
                return self._send(200, {"data": {"access_token": TOKEN, "expires_in": 3600}})
            return self._send(401, {"error": "wrong username or password"})
        if self.path == "/v1/messages":
            if self.headers.get("Authorization") != "Bearer " + TOKEN:
                return self._send(401, {"error": "log in first"})
            return self._send(200, {"status": "queued", "message_id": uuid.uuid4().hex[:12]})
        self._send(404, {"error": "not found"})


HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
