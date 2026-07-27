package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"firebase.google.com/go/v4/messaging"
	"github.com/appleboy/go-fcm"
	uuid "github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"

	httptrace "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	dd_logrus "github.com/DataDog/dd-trace-go/contrib/sirupsen/logrus/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var (
	client                    *fcm.Client
	configListenAddr          string
	configCredentialsFilePath string
	configMaxQueueSize        int
	configMaxWorkers          int
	messageChan               chan *Message
	ctx                       context.Context
)

func main() {
	var err error

	err = tracer.Start()
	if err != nil {
		log.Fatal(fmt.Sprintf("Error starting tracer: %s", err))
		return
	}
	defer tracer.Stop()

	mux := httptrace.NewServeMux()

	log.AddHook(&dd_logrus.DDContextLogHook{})

	flag.StringVar(&configListenAddr, "bind", "127.0.0.1:42069", "Bind address")
	flag.StringVar(&configCredentialsFilePath, "credentials-file-path", "", "Path to the Firebase credentials file")
	flag.IntVar(&configMaxQueueSize, "max-queue-size", 1024, "Maximum number of messages to queue")
	flag.IntVar(&configMaxWorkers, "max-workers", 4, "Maximum number of workers")
	flag.Parse()

	if configCredentialsFilePath == "" {
		log.Fatal("Firebase server key not provided")
	}

	ctx = context.Background()
	client, err = fcm.NewClient(ctx, fcm.WithCredentialsFile(configCredentialsFilePath))
	if err != nil {
		log.Fatal(fmt.Sprintf("Error setting up FCM client: %s", err))
	}

	// create workers
	messageChan = make(chan *Message, configMaxQueueSize)
	for i := 1; i <= configMaxWorkers; i++ {
		go fcmNotificationsWorker(i)
	}

	mux.HandleFunc("/relay-to/", handler)

	log.Info(fmt.Sprintf("Starting on %s...", configListenAddr))
	log.Fatal(http.ListenAndServe(configListenAddr, mux))
}

func nextRequestID() string {
	return uuid.NewV4().String()
}

func handler(writer http.ResponseWriter, request *http.Request) {
	span, sctx := tracer.StartSpanFromContext(ctx, "web.request", tracer.ResourceName(request.RequestURI))
	defer span.Finish()

	requestID := nextRequestID()
	requestLog := log.WithFields(log.Fields{"request-id": requestID}).WithContext(sctx)

	writer.Header().Set("X-Request-Id", requestID)

	components := strings.Split(request.URL.Path, "/")

	if len(components) < 4 {
		http.Error(writer, "Invalid URL path", http.StatusBadRequest)
		requestLog.Error(fmt.Sprintf("Invalid URL path: %s", request.URL.Path))
		return
	}

	if components[2] != "fcm" {
		http.Error(writer, "Invalid target environment", http.StatusBadRequest)
		requestLog.Error(fmt.Sprintf("Invalid target environment: %s", components[2]))
		return
	}

	deviceToken := components[3]
	if deviceToken == "" {
		http.Error(writer, "Missing device token", http.StatusBadRequest)
		requestLog.Error("Missing device token")
		return
	}

	buffer := new(bytes.Buffer)
	_, err := buffer.ReadFrom(request.Body)
	if err != nil {
		http.Error(writer, "Error reading request body", http.StatusInternalServerError)
		requestLog.Error(fmt.Sprintf("Error reading request body: %s", err))
		return
	}

	encodedString := encode85(buffer.Bytes())

	fcmMessage := &messaging.Message{
		Token:   deviceToken,
		Android: &messaging.AndroidConfig{},
		Data: map[string]string{
			"p": encodedString,
		},
		Notification: &messaging.Notification{
			Title: "🎺",
		},
		APNS: &messaging.APNSConfig{
			Payload: &messaging.APNSPayload{
				Aps: &messaging.Aps{
					ContentAvailable: true,
					MutableContent:   true,
				},
			},
		},
	}

	if len(components) > 4 {
		fcmMessage.Data["x"] = strings.Join(components[4:], "/")
	}

	switch request.Header.Get("Content-Encoding") {
	case "aesgcm":
		if publicKey, err := encodedValue(request.Header, "Crypto-Key", "dh"); err == nil {
			fcmMessage.Data["k"] = publicKey
		} else {
			http.Error(writer, "Error retrieving public key", http.StatusBadRequest)
			requestLog.Error(fmt.Sprintf("Error retrieving public key: %s", err))
			return
		}

		if salt, err := encodedValue(request.Header, "Encryption", "salt"); err == nil {
			fcmMessage.Data["s"] = salt
		} else {
			http.Error(writer, "Error retrieving salt", http.StatusBadRequest)
			requestLog.Error(fmt.Sprintf("Error retrieving salt: %s", err))
			return
		}
	case "aes128gcm":
		fcmMessage.Data["rfc"] = "1"
	default:
		http.Error(writer, "Unsupported content encoding", http.StatusUnsupportedMediaType)
		requestLog.Error(fmt.Sprintf("Unsupported content encoding: %s", request.Header.Get("Content-Encoding")))
		return
	}

	if seconds := request.Header.Get("TTL"); seconds != "" {
		if ttl, err := strconv.Atoi(seconds); err == nil {
			timeToLive := time.Duration(ttl) * time.Second
			fcmMessage.Android.TTL = &timeToLive
		}
	}

	if topic := request.Header.Get("Topic"); topic != "" {
		fcmMessage.Android.CollapseKey = topic
	}

	switch request.Header.Get("Urgency") {
	case "very-low", "low":
		fcmMessage.Android.Priority = "normal"
	default:
		fcmMessage.Android.Priority = "high"
	}

	messageChan <- &Message{
		unsubscribeUrl: request.Header.Get("Unsubscribe-URL"),
		fcmMessage:     fcmMessage,
		requestLog:     requestLog,
		ctx:            sctx,
	}

	writer.WriteHeader(201)

	requestLog.WithFields(log.Fields{
		"to":           fcmMessage.Token,
		"priority":     fcmMessage.Android.Priority,
		"ttl":          fcmMessage.Android.TTL,
		"collapse-key": fcmMessage.Android.CollapseKey,
	}).Info("Queue success")
}

func encodedValue(header http.Header, name, key string) (string, error) {
	keyValues := parseKeyValues(header.Get(name))
	value, exists := keyValues[key]
	if !exists {
		return "", fmt.Errorf("value %s not found in header %s", key, name)
	}

	bytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}

	return encode85(bytes), nil
}

func parseKeyValues(values string) map[string]string {
	f := func(c rune) bool {
		return c == ';'
	}

	entries := strings.FieldsFunc(values, f)

	m := make(map[string]string)
	for _, entry := range entries {
		parts := strings.Split(entry, "=")
		m[parts[0]] = parts[1]
	}

	return m
}

var z85digits = []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.-:+=^!/*?&<>()[]{}@%$#")

func encode85(bytes []byte) string {
	numBlocks := len(bytes) / 4
	suffixLength := len(bytes) % 4

	encodedLength := numBlocks * 5
	if suffixLength != 0 {
		encodedLength += suffixLength + 1
	}

	encodedBytes := make([]byte, encodedLength)

	src := bytes
	dest := encodedBytes
	for block := 0; block < numBlocks; block++ {
		value := binary.BigEndian.Uint32(src)

		for i := 0; i < 5; i++ {
			dest[4-i] = z85digits[value%85]
			value /= 85
		}

		src = src[4:]
		dest = dest[5:]
	}

	if suffixLength != 0 {
		value := 0

		for i := 0; i < suffixLength; i++ {
			value *= 256
			value |= int(src[i])
		}

		for i := 0; i < suffixLength+1; i++ {
			dest[suffixLength-i] = z85digits[value%85]
			value /= 85
		}
	}

	return string(encodedBytes)
}
