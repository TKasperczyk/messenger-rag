package storage

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// E2EEMetadata stores E2EE-related metadata in our sync_metadata table
type E2EEMetadata struct {
	DeviceID     uint16
	FacebookUUID uuid.UUID
	Registered   bool
}

// SaveE2EEMetadata saves E2EE metadata to our database
func (s *Storage) SaveE2EEMetadata(meta *E2EEMetadata) error {
	if err := s.SetSyncMetadata("e2ee_device_id", fmt.Sprintf("%d", meta.DeviceID)); err != nil {
		return err
	}
	if err := s.SetSyncMetadata("e2ee_facebook_uuid", meta.FacebookUUID.String()); err != nil {
		return err
	}
	if err := s.SetSyncMetadata("e2ee_registered", fmt.Sprintf("%t", meta.Registered)); err != nil {
		return err
	}
	return nil
}

// GetE2EEMetadata retrieves E2EE metadata from our database
func (s *Storage) GetE2EEMetadata() (*E2EEMetadata, error) {
	meta := &E2EEMetadata{}

	deviceIDStr, err := s.GetSyncMetadata("e2ee_device_id")
	if err != nil {
		return nil, err
	}
	if deviceIDStr != "" {
		var deviceID int
		fmt.Sscanf(deviceIDStr, "%d", &deviceID)
		meta.DeviceID = uint16(deviceID)
	}

	uuidStr, err := s.GetSyncMetadata("e2ee_facebook_uuid")
	if err != nil {
		return nil, err
	}
	if uuidStr != "" {
		parsed, parseErr := uuid.Parse(uuidStr)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid facebook_uuid in metadata: %w", parseErr)
		}
		meta.FacebookUUID = parsed
	}

	registeredStr, err := s.GetSyncMetadata("e2ee_registered")
	if err != nil {
		return nil, err
	}
	meta.Registered = registeredStr == "true"

	return meta, nil
}

// GetDB returns the underlying database connection for whatsmeow
func (s *Storage) GetDB() *sql.DB {
	return s.db
}
