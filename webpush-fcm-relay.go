package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"flag"
	"net/http"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/appleboy/go-fcm"
	"github.com/satori/go.uuid"
)

var (
	client             *fcm.Client
	configListenAddr   string
	configServerKey    string
	configMaxQueueSize int
	configMaxWorkers   int
	messageChan        chan *fcm.Message
)

func main() {
	flag.StringVar(&configListenAddr, "bind", "127.0.0.1:42069", "Bind address")
	flag.StringVar(&configServerKey, "server-key", "", "Firebase server key")
	flag.IntVar(&configMaxQueueSize, "max-queue-size", 1024, "Maximum number of messages to queue")
	flag.IntVar(&configMaxWorkers, "max-workers", 4, "Maximum number of workers")
	flag.Parse()

	if configServerKey == "" {
		log.Fatal("Firebase server key not provided")
	}

	_client, err := fcm.NewClient(configServerKey)
	if err != nil {
		log.Fatal(fmt.Sprintf("Error setting up FCM client: %s", err))
	}

	client = _client

	// create workers
	messageChan = make(chan *fcm.Message, configMaxQueueSize)
	for i := 1; i <= configMaxWorkers; i++ {
		go worker(i)
	}

	http.HandleFunc("/relay-to/", handler)

	log.Info(fmt.Sprintf("Starting on %s...", configListenAddr))
	log.Fatal(http.ListenAndServe(configListenAddr, nil))
}

func httpError(writer http.ResponseWriter, code int, message string) {
	writer.WriteHeader(code)
	fmt.Fprintln(writer, message)
	log.Println(message)
}

func nextRequestID() string {
	return uuid.NewV4().String()
}

func handler(writer http.ResponseWriter, request *http.Request) {
	requestID := nextRequestID()
	requestLog := log.WithFields(log.Fields{"request-id": requestID})

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
	buffer.ReadFrom(request.Body)
	encodedString := encode85(buffer.Bytes())

	message := &fcm.Message{
		To: deviceToken,
		Data: map[string]interface{}{
			"p": encodedString,
		},
		MutableContent:   true,
		ContentAvailable: true,
		Notification: &fcm.Notification{
			Title: "🎺",
		},
	}

	if len(components) > 4 {
		message.Data["x"] = strings.Join(components[4:], "/")
	}

	switch request.Header.Get("Content-Encoding") {
	case "aesgcm":
		if publicKey, err := encodedValue(request.Header, "Crypto-Key", "dh"); err == nil {
			message.Data["k"] = publicKey
		} else {
			http.Error(writer, "Error retrieving public key", http.StatusBadRequest)
			requestLog.Error(fmt.Sprintf("Error retrieving public key: %s", err))
			return
		}

		if salt, err := encodedValue(request.Header, "Encryption", "salt"); err == nil {
			message.Data["s"] = salt
		} else {
			http.Error(writer, "Error retrieving salt", http.StatusBadRequest)
			requestLog.Error(fmt.Sprintf("Error retrieving salt: %s", err))
			return
		}
	default:
		http.Error(writer, "Unsupported content encoding", http.StatusUnsupportedMediaType)
		requestLog.Error(fmt.Sprintf("Unsupported content encoding: %s", request.Header.Get("Content-Encoding")))
		return
	}

	if seconds := request.Header.Get("TTL"); seconds != "" {
		if ttl, err := strconv.Atoi(seconds); err == nil {
			timeToLive := uint(ttl)
			message.TimeToLive = &timeToLive
		}
	}

	if topic := request.Header.Get("Topic"); topic != "" {
		message.CollapseKey = topic
	}

	switch request.Header.Get("Urgency") {
	case "very-low", "low":
		message.Priority = "normal"
	default:
		message.Priority = "high"
	}

	messageChan <- message

	writer.WriteHeader(201)

	requestLog.WithFields(log.Fields{
		"to":           message.To,
		"priority":     message.Priority,
		"ttl":          message.TimeToLive,
		"collapse-key": message.CollapseKey,
	}).Info("Queue success")
}

func worker(wid int) {
	log.Info(fmt.Sprintf("Starting worker %d", wid))
	for msg := range messageChan {
		_, err := client.Send(msg)
		if err != nil {
			log.Error(fmt.Sprintf("error sending ftm message: %s", err.Error()))
		}
	}
	log.Info(fmt.Sprintf("Worker %d stopped", wid))
}

func encodedValue(header http.Header, name, key string) (string, error) {
	keyValues := parseKeyValues(header.Get(name))
	value, exists := keyValues[key]
	if !exists {
		return "", errors.New(fmt.Sprintf("Value %s not found in header %s", key, name))
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
