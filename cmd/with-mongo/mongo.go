package main

import (
	"context"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

type Connection struct {
	// Used in Connector to differentiate connects from other in case of connection error
	name string

	uri string

	*mongo.Database
}

func New(name string, uri string) *Connection {
	return &Connection{
		name: name,
		uri:  uri,
	}
}

func (c *Connection) Name() string {
	return c.name
}

func (c *Connection) Connect(ctx context.Context) (err error) {
	config, err := connstring.Parse(c.uri)
	if err != nil {
		return errors.Wrap(err, "connection string parse")
	}

	client, err := mongo.Connect(
		ctx,
		options.Client().ApplyURI(c.uri),
	)
	if err != nil {
		return errors.Wrap(err, "connect")
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "ping")
	}

	c.Database = client.Database(config.Database)

	return nil
}

func (c *Connection) Disconnect(ctx context.Context) error {
	return c.Client().Disconnect(ctx)
}

func (c *Connection) Close() error {
	return c.Disconnect(context.Background())
}
