package config

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mc0239/logm"

	"go.etcd.io/etcd/client"
)

type etcdConfigSource struct {
	client          *client.Client
	startRetryDelay int64
	maxRetryDelay   int64
	path            string
	logger          *logm.Logm
}

func newEtcdConfigSource(localConfig configSource, lgr *logm.Logm) configSource {
	var etcdConfig etcdConfigSource
	lgr.Verbose("Initializing %s config source", etcdConfig.Name())
	etcdConfig.logger = lgr

	clientConfig := client.Config{}

	etcdAddress := localConfig.Get("kumuluzee.config.etcd.hosts")
	if etcdAddress != nil {
		clientConfig.Endpoints = []string{etcdAddress.(string)}
	}

	cl, err := client.New(clientConfig)
	if err != nil {
		lgr.Error("Failed to create etcd client: %s", err.Error())
		return nil
	}

	lgr.Info("etcd client address set to %v", clientConfig.Endpoints)

	etcdConfig.client = &cl

	envName := localConfig.Get("kumuluzee.env.name")
	if envName == nil {
		envName = "dev"
	}
	name := localConfig.Get("kumuluzee.name")
	version := localConfig.Get("kumuluzee.version")
	if version == nil {
		version = "1.0.0"
	}

	startRetryDelay, ok := localConfig.Get("kumuluzee.config.start-retry-delay-ms").(float64)
	if !ok {
		lgr.Warning("Failed to assert value kumuluzee.config.start-retry-delay-ms as float64. Using default value 500.")
		startRetryDelay = 500
	}
	etcdConfig.startRetryDelay = int64(startRetryDelay)

	maxRetryDelay, ok := localConfig.Get("kumuluzee.config.max-retry-delay-ms").(float64)
	if !ok {
		lgr.Warning("Failed to assert value kumuluzee.config.max-retry-delay-ms as float64. Using default value 900000.")
		maxRetryDelay = 900000
	}
	etcdConfig.maxRetryDelay = int64(maxRetryDelay)

	etcdConfig.path = fmt.Sprintf("environments/%s/services/%s/%s/config", envName, name, version)

	lgr.Info("etcd key-value namespace: %s", etcdConfig.path)

	lgr.Verbose("Initialized %s config source", etcdConfig.Name())
	return etcdConfig
}

func (c etcdConfigSource) ordinal() int {
	return 150
}

func (c etcdConfigSource) Get(key string) interface{} {
	kv := client.NewKeysAPI(*c.client)

	key = strings.Replace(key, ".", "/", -1)
	//fmt.Printf("KV path: %s\n", path.Join(c.path, key))

	resp, err := kv.Get(context.Background(), path.Join(c.path, key), nil)
	if err != nil {
		c.logger.Warning("Error getting value: %v", err)
		return nil
	}

	return resp.Node.Value
}

func (c etcdConfigSource) Subscribe(key string, callback func(key string, value string)) {
	c.logger.Info("Creating a watch for key %s, source: %s", key, c.Name())
	go c.watch(key, "", c.startRetryDelay, callback)
}

func (c etcdConfigSource) watch(key string, previousValue string, retryDelay int64, callback func(key string, value string)) {

	// TODO: have a parameter for watch duration, (likely reads from config.yaml?)
	t := 10 * time.Minute
	c.logger.Verbose("Set a watch on key %s with %s wait time", key, t)
	// TODO: where is timeout set????


	key = strings.Replace(key, ".", "/", -1)
	kv := client.NewKeysAPI(*c.client)

	watcher := kv.Watcher(path.Join(c.path, key), nil)

	resp, err := watcher.Next(context.Background())
	if err != nil {
		c.logger.Warning("Watch on %s failed with error: %s, retry delay: %d ms", key, err.Error(), retryDelay)

		// sleep for current delay
		time.Sleep(time.Duration(retryDelay) * time.Millisecond)

		// exponentially extend retry delay, but keep it at most maxRetryDelay
		newRetryDelay := retryDelay * 2
		if newRetryDelay > c.maxRetryDelay {
			newRetryDelay = c.maxRetryDelay
		}
		c.watch(key, "", newRetryDelay, callback)
		return
	}

	c.logger.Verbose("Wait time (%s) on watch for key %s reached.", key, t)

	if string(resp.Node.Value) != previousValue {
		callback(key, string(resp.Node.Value))
	}
	c.watch(key, string(resp.Node.Value), c.startRetryDelay, callback)
}

func (c etcdConfigSource) Name() string {
	return "etcd"
}
