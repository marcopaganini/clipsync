// This file is part of clipsync (C)2022 by Marco Paganini
// Please see http://github.com/marcopaganini/clipsync for details.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func newBroker(cfg globalConfig, handler func(client mqtt.Client, msg mqtt.Message)) (mqtt.Client, error) {
	tlsconfig, err := newTLSConfig(*cfg.cafile)
	if err != nil {
		return nil, err
	}
	opts := mqtt.NewClientOptions()
	opts.AddBroker(*cfg.server)

	// Client ID must be unique.
	id := uuid.New()
	clientID := "clipsync-" + id.String()
	opts.SetClientID(clientID)
	log.Debugf("Set MQTT Client ID to %v", clientID)

	opts.SetKeepAlive(4 * time.Second)
	opts.SetTLSConfig(tlsconfig)
	opts.SetPingTimeout(2 * time.Second)
	opts.SetAutoReconnect(true)

	if *cfg.user != "" {
		opts.SetUsername(*cfg.user)
	}
	if *cfg.password != "" {
		opts.SetPassword(*cfg.password)
	}

	// If handler is present, assume we'll subscribe to a topic. In this case,
	// set OnConnectHandler to re-subscribe every time we have a connection.
	// This, together with SetAutoReconnect guarantees that we'll keep
	// receiving messages from the topic after an automatic reconnect.
	if handler != nil {
		opts.SetOnConnectHandler(func(onconn mqtt.Client) {
			log.Debugf("Connection detected. Subscribing to topic: %q", *cfg.topic)
			if token := onconn.Subscribe(*cfg.topic, 0, handler); token.Wait() && token.Error() != nil {
				log.Errorf("Unable to subscribe to topic %s: %v", *cfg.topic, token.Error())
			}
		})
	}

	c := mqtt.NewClient(opts)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	return c, nil
}

func newTLSConfig(cafile string) (*tls.Config, error) {
	// Create tls.Config with desired tls properties
	ret := &tls.Config{
		ClientAuth: tls.NoClientCert,
		// ClientCAs = certs used to validate client cert.
		ClientCAs: nil,
		// InsecureSkipVerify = Cert contents must match server, IP, host, etc.
		//InsecureSkipVerify: true,
	}
	if cafile != "" {
		certpool := x509.NewCertPool()
		pemCerts, err := os.ReadFile(cafile)
		if err == nil {
			certpool.AppendCertsFromPEM(pemCerts)
		}
		ret.RootCAs = certpool
	}
	return ret, nil
}
