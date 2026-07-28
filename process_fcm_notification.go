package main

import (
	"context"
	"fmt"
	"net/http"

	"firebase.google.com/go/v4/messaging"
	log "github.com/sirupsen/logrus"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type Message struct {
	unsubscribeUrl string
	fcmMessage     *messaging.Message
	requestLog     *log.Entry // For logging with datadog context
	ctx            context.Context
}

func fcmNotificationsWorker(wid int, httpClient *http.Client) {
	log.Info(fmt.Sprintf("Starting worker %d", wid))

	for msg := range messageChan {
		processFcmNotificationMessage(msg, httpClient, wid)
	}

	log.Info(fmt.Sprintf("Worker %d stopped", wid))
}

func processFcmNotificationMessage(msg *Message, httpClient *http.Client, workerId int) {
	span, sctx := tracer.StartSpanFromContext(msg.ctx,
		"processMessage",
		tracer.Tag("workerId", workerId),
		tracer.Tag("deviceToken", msg.fcmMessage.Token),
	)
	defer span.Finish()

	requestLog := msg.requestLog.WithFields(log.Fields{"worker-id": workerId})
	resp, err := client.Send(sctx, msg.fcmMessage)
	if err != nil {
		requestLog.Error(fmt.Sprintf("error sending fcm message: %s", err.Error()))
	}

	if resp.FailureCount > 0 {
		for _, resp := range resp.Responses {
			if !resp.Success {
				unsubscribed := false

				if msg.unsubscribeUrl != "" && resp.Error.Error() == "NotRegistered" {
					unsubscribed = unsubscribe(sctx, msg.unsubscribeUrl, httpClient, requestLog)
				}

				requestLog.
					WithFields(log.Fields{"message-id": resp.MessageID, "unsubscribed": unsubscribed}).
					Warn(fmt.Sprintf("message rejected: %s", resp.Error))
			}
		}
	}
}

func unsubscribe(ctx context.Context, unsubscribeUrl string, httpClient *http.Client, requestLog *log.Entry) bool {
	span, sctx := tracer.StartSpanFromContext(ctx, "unsubscribe",
		tracer.Tag("unsubscribeUrl", unsubscribeUrl),
	)
	defer span.Finish()

	unsubscribeReq, reqErr := http.NewRequestWithContext(sctx, "DELETE", unsubscribeUrl, nil)
	unsubscribeReq.Header.Set("User-Agent", userAgent())

	if reqErr != nil {
		requestLog.WithFields(log.Fields{
			"error":           reqErr.Error(),
			"unsubscribe-url": unsubscribeUrl,
		}).Error("Failed to create HTTP request for unsubscribe")

		return false
	}

	unsubscribeResp, respErr := httpClient.Do(unsubscribeReq)
	if respErr != nil {
		requestLog.WithFields(log.Fields{
			"error":           respErr.Error(),
			"unsubscribe-url": unsubscribeUrl,
		}).Error("Failed to send unsubscribe request")
		return false
	}

	if unsubscribeResp.StatusCode == 200 {
		return true
	} else {
		requestLog.WithFields(log.Fields{
			"status-code":     unsubscribeResp.StatusCode,
			"unsubscribe-url": unsubscribeUrl,
		}).Error(fmt.Sprintf("Failed to unsubscribe for notification (%v)", unsubscribeResp.StatusCode))

		return false
	}
}
