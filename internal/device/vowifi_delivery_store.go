package device

import (
	"errors"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/messaging"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"gorm.io/gorm"
)

type vowifiDeliveryStore struct{}

func (vowifiDeliveryStore) CreateSMSDelivery(messageID, imsi, deviceID, peer, content string, partsTotal int, at time.Time) error {
	return db.CreateSMSDelivery(messageID, imsi, deviceID, peer, content, partsTotal, at)
}

func (vowifiDeliveryStore) UpsertSMSDeliveryPart(messageID string, partNo int, callID string, rpMR int, state string, sentAt time.Time) error {
	return db.UpsertSMSDeliveryPart(messageID, partNo, callID, rpMR, state, sentAt)
}

func (vowifiDeliveryStore) MarkSMSDeliveryPartReport(inReplyTo, callID, deviceID string, rpMR int, state string, sipCode int, rpCause int, errText string, at time.Time) (messaging.DeliveryPartMatch, error) {
	part, err := db.MarkSMSDeliveryPartReport(inReplyTo, callID, deviceID, rpMR, state, sipCode, rpCause, errText, at)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return messaging.DeliveryPartMatch{}, messaging.ErrDeliveryNotFound
		}
		return messaging.DeliveryPartMatch{}, err
	}
	return messaging.DeliveryPartMatch{
		MessageID: part.MessageID,
		PartNo:    part.PartNo,
		State:     part.State,
	}, nil
}

func (vowifiDeliveryStore) RecomputeSMSDelivery(messageID string, at time.Time) error {
	return db.RecomputeSMSDelivery(messageID, at)
}

func (vowifiDeliveryStore) UpdateSMSDeliveryState(messageID, state, lastError string, acks int, at time.Time) error {
	return db.UpdateSMSDeliveryState(messageID, state, lastError, acks, at)
}

func (vowifiDeliveryStore) GetSMSDeliveryStatus(messageID string) (*messaging.DeliveryStatus, error) {
	status, err := db.GetSMSDeliveryStatus(messageID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, messaging.ErrDeliveryNotFound
		}
		return nil, err
	}
	out := &messaging.DeliveryStatus{
		MessageID:  status.MessageID,
		IMSI:       status.IMSI,
		DeviceID:   status.DeviceID,
		Peer:       status.Peer,
		Content:    status.Content,
		PartsTotal: status.PartsTotal,
		Acks:       status.Acks,
		State:      status.State,
		LastError:  status.LastError,
		CreatedAt:  status.CreatedAt,
		UpdatedAt:  status.UpdatedAt,
		Parts:      make([]messaging.DeliveryPartStatus, 0, len(status.Parts)),
	}
	for _, p := range status.Parts {
		out.Parts = append(out.Parts, messaging.DeliveryPartStatus{
			PartNo:      p.PartNo,
			CallID:      p.CallID,
			InReplyTo:   p.InReplyTo,
			RPMR:        p.RPMR,
			State:       p.State,
			SIPCode:     p.SIPCode,
			RPCause:     p.RPCause,
			RPCauseText: messaging.RPCauseText(p.RPCause),
			ErrorText:   p.ErrorText,
			SentAt:      p.SentAt,
			ReportAt:    p.ReportAt,
			CreatedAt:   p.CreatedAt,
			UpdatedAt:   p.UpdatedAt,
		})
	}
	return out, nil
}
