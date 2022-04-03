webpush-fcm-relay
=================

Relay encrypted WebPush notifications to Firebase Cloud Messaging.

## Usage

```
Usage of ./webpush-fcm-relay:
  -bind string
      Bind address (default "127.0.0.1:42069")
  -server-key string
      Firebase server key
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
