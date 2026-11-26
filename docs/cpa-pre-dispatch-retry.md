# CPA Host Model: confirmed pre-dispatch failures

KCR's rule-level `failover.retry_before_switch` is opt-in. Stock hosts that
do not send the optional signal below retain the old failover behavior, even
when the setting is enabled. Roll out host support before advertising the
setting as available for a deployment.

For a pinned `host.model.execute` or `host.model.execute_stream` call, the
trusted CPA host may include a boolean in its outer error envelope:

```json
{
  "ok": false,
  "error": {
    "code": "host_call_failed",
    "message": "upstream connection failed before sending the inference request",
    "http_status": 500,
    "request_not_sent": true
  }
}
```

This boolean is an assertion about the **entire nested execution**, not just the
last dial attempt. Emit it only when a local network failure prevented any
inference submission to an upstream, including through redirects, retries,
protocol fallback, and any candidate or model alias attempts. Do not copy it
from upstream bodies/headers, derive it from error strings or HTTP status, or
infer it merely because no response bytes arrived. If a transport cannot prove
non-delivery, omit the field. An error while reading an already-open stream
is not eligible. KCR still requires the exact pin to have been claimed and never
treats the flag as permission to bypass scheduler ownership.

To validate a CPA producer, use a local upstream and exercise dial failure
before an initial write (true), collision with an actual HTTP 500 response
(false), request received followed by disconnect (false), and a multi-attempt
execution where an earlier request was sent but the final dial failed (false).
For KCR itself, `src/abi_failover_smoke.py` checks the trusted positive marker,
absence, and response-stream boundaries against its compiled C ABI.
