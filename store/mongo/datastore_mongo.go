// Copyright 2020 Northern.tech AS
//
//    All Rights Reserved

package mongo

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/mendersoftware/go-lib-micro/config"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mopts "go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"

	dconfig "github.com/mendersoftware/deviceconnect/config"
)

const (
	// DevicesCollectionName refers to the collection of stored devices
	DevicesCollectionName = "devices"
)

// SetupDataStore returns the mongo data store and optionally runs migrations
func SetupDataStore(automigrate bool) (*DataStoreMongo, error) {
	ctx := context.Background()
	dbClient, err := NewClient(ctx, config.Config)
	if err != nil {
		return nil, errors.New(fmt.Sprintf("failed to connect to db: %v", err))
	}
	err = doMigrations(ctx, dbClient, automigrate)
	if err != nil {
		return nil, err
	}
	dataStore := NewDataStoreWithClient(dbClient, config.Config)
	return dataStore, nil
}

func doMigrations(ctx context.Context, client *mongo.Client,
	automigrate bool) error {
	db := config.Config.GetString(dconfig.SettingDbName)
	err := Migrate(ctx, db, DbVersion, client, automigrate)
	if err != nil {
		return errors.New(fmt.Sprintf("failed to run migrations: %v", err))
	}

	return nil
}

func disconnectClient(parentCtx context.Context, client *mongo.Client) {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	client.Disconnect(ctx)
	<-ctx.Done()
	cancel()
}

// NewClient returns a mongo client
func NewClient(ctx context.Context, c config.Reader) (*mongo.Client, error) {

	clientOptions := mopts.Client()
	mongoURL := c.GetString(dconfig.SettingMongo)
	if !strings.Contains(mongoURL, "://") {
		return nil, errors.Errorf("Invalid mongoURL %q: missing schema.",
			mongoURL)
	}
	clientOptions.ApplyURI(mongoURL)

	username := c.GetString(dconfig.SettingDbUsername)
	if username != "" {
		credentials := mopts.Credential{
			Username: c.GetString(dconfig.SettingDbUsername),
		}
		password := c.GetString(dconfig.SettingDbPassword)
		if password != "" {
			credentials.Password = password
			credentials.PasswordSet = true
		}
		clientOptions.SetAuth(credentials)
	}

	if c.GetBool(dconfig.SettingDbSSL) {
		tlsConfig := &tls.Config{}
		tlsConfig.InsecureSkipVerify = c.GetBool(dconfig.SettingDbSSLSkipVerify)
		clientOptions.SetTLSConfig(tlsConfig)
	}

	// Set writeconcern to acknowlage after write has propagated to the
	// mongod instance and commited to the file system journal.
	var wc *writeconcern.WriteConcern
	wc.WithOptions(writeconcern.W(1), writeconcern.J(true))
	clientOptions.SetWriteConcern(wc)

	// Set 10s timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to mongo server")
	}

	// Validate connection
	if err = client.Ping(ctx, nil); err != nil {
		return nil, errors.Wrap(err, "Error reaching mongo server")
	}

	return client, nil
}

// DataStoreMongo is the data storage service
type DataStoreMongo struct {
	// client holds the reference to the client used to communicate with the
	// mongodb server.
	client *mongo.Client
	// dbName contains the name of the auditlogs database.
	dbName string
}

// NewDataStoreWithClient initializes a DataStore object
func NewDataStoreWithClient(client *mongo.Client, c config.Reader) *DataStoreMongo {
	dbName := c.GetString(dconfig.SettingDbName)

	return &DataStoreMongo{
		client: client,
		dbName: dbName,
	}
}

// Ping verifies the connection to the database
func (db *DataStoreMongo) Ping(ctx context.Context) error {
	res := db.client.Database(DbName).RunCommand(ctx, bson.M{"ping": 1})
	return res.Err()
}

// Close disconnects the client
func (db *DataStoreMongo) Close() {
	ctx := context.Background()
	disconnectClient(ctx, db.client)
}

func (db *DataStoreMongo) dropDatabase() error {
	ctx := context.Background()
	err := db.client.Database(db.dbName).Drop(ctx)
	return err
}