package pubsub

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
	zmq "github.com/pebbe/zmq3"
	"strings"
	"sync"
	"time"
)

// A pub-sub message - defined to support Redis receiving different
// message types, such as subscribe/unsubscribe info.
type Message struct {
	Type    string
	Channel string
	Data    string
}

// Client interface for both Redis and ZMQ pubsub clients.
type Client interface {
	Subscribe(channels ...interface{}) (err error)
	Unsubscribe(channels ...interface{}) (err error)
	Publish(channel string, message string) error
	Receive() (Message, error)
}

// Redis client - defines the underlying connection and pub-sub
// connections, as well as a mutex for locking write access,
// since this occurs from multiple goroutines.
type RedisClient struct {
	conn redis.Conn
	redis.PubSubConn
	sync.Mutex
}

// ZMQ client - just defines the pub and sub ZMQ sockets.
type ZMQClient struct {
	ctx *zmq.Context
	pub *zmq.Socket
	sub *zmq.Socket
}

// Returns a new Redis client. The underlying redigo package uses
// Go's bufio package which will flush the connection when it contains
// enough data to send, but we still need to set up some kind of timed
// flusher, so it's done here with a goroutine.
func NewRedisClient(host string) *RedisClient {
	host = fmt.Sprintf("%s:6379", host)
	conn, _ := redis.Dial("tcp", host)
	pubsub, _ := redis.Dial("tcp", host)
	client := RedisClient{conn, redis.PubSubConn{pubsub}, sync.Mutex{}}
	go func() {
		for {
			time.Sleep(200 * time.Millisecond)
			client.Lock()
			client.conn.Flush()
			client.Unlock()
		}
	}()
	return &client
}

func (client *RedisClient) Publish(channel, message string) error {
	client.Lock()
	client.conn.Send("PUBLISH", channel, message)
	client.Unlock()

	return nil
}

func (client *RedisClient) Receive() (Message, error) {
	switch message := client.PubSubConn.Receive().(type) {
	case redis.Message:
		return Message{"message", message.Channel, string(message.Data)}, nil
	case redis.Subscription:
		return Message{message.Kind, message.Channel, string(message.Count)}, nil
	}
	return Message{}, nil
}

func NewZMQClient(host string) (*ZMQClient, error) {
	var err error
	var context *zmq.Context
	context, err = zmq.NewContext()
	if err != nil {
		return nil, err
	}
	var pub *zmq.Socket
	pub, err = context.NewSocket(zmq.PUSH)
	if err != nil {
		return nil, err
	}
	pub.Connect(fmt.Sprintf("tcp://%s:%d", host, 5562))
	var sub *zmq.Socket
	sub, err = context.NewSocket(zmq.SUB)
	if err != nil {
		return nil, err
	}
	sub.Connect(fmt.Sprintf("tcp://%s:%d", host, 5561))
	return &ZMQClient{context, pub, sub}, nil
}

func (client *ZMQClient) Subscribe(channels ...interface{}) error {
	for _, channel := range channels {
		if err := client.sub.SetSubscribe(channel.(string)); err != nil {
			return err
		}
	}
	return nil
}

func (client *ZMQClient) Unsubscribe(channels ...interface{}) error {
	for _, channel := range channels {
		if err := client.sub.SetUnsubscribe(channel.(string)); err != nil {
			return err
		}
	}
	return nil
}

func (client *ZMQClient) Publish(channel, message string) error {
	_, err := client.pub.Send(channel+" "+message, 0)
	return err
}

func (client *ZMQClient) Receive() (Message, error) {
	message, err := client.sub.Recv(0)
	if err != nil {
		return Message{}, err
	}
	parts := strings.SplitN(string(message), " ", 2)
	return Message{Type: "message", Channel: parts[0], Data: parts[1]}, nil
}
