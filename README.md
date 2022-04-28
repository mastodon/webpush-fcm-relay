webpush-fcm-relay
=================

[![Go](https://github.com/mastodon/webpush-fcm-relay/actions/workflows/go.yml/badge.svg)](https://github.com/mastodon/webpush-fcm-relay/actions/workflows/go.yml)

Relay encrypted WebPush notifications to Firebase Cloud Messaging.

## Usage

```
Usage of ./webpush-fcm-relay:
  -bind string
      Bind address (default "127.0.0.1:42069")
  -server-key string
      Firebase server key
  -max-queue-size int (default 1024)
      The size of the internal queue
  -max-workers int (default 4)
      The number of workers sending requests to fcm
```

## API

Send a request to `POST /relay-to/fcm/:device_token(/:extra)` with the encrypted payload in the body and content encoding `aesgcm`.

Required headers:

- `Content-Encoding`
- `Crypto-Key`
- `Encryption`

Supported headers:

- `TTL`
- `Topic`
- `Urgency`

## More information

See [toot-relay](https://github.com/DagAgren/toot-relay)
