// Copyright (c) 2010 The Grumble Authors
// The use of this source code is goverened by a BSD-style
// license that can be found in the LICENSE-file.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"mumble.info/grumble/pkg/blobstore"
	"mumble.info/grumble/pkg/logtarget"
)

var servers map[int64]*Server
var blobStore blobstore.BlobStore

func main() {
	var err error

	flag.Parse()
	if Args.ShowHelp == true {
		Usage()
		return
	}

	runtimeConfig, err = LoadRuntimeConfig()
	if err != nil {
		emitBootstrapEvent("error", "configuration_invalid", map[string]string{
			"listener_type": "runtime",
			"reason":        err.Error(),
		})
		log.Fatalf("Invalid runtime configuration: %v", err)
	}
	Args.DataDir = runtimeConfig.DataDir
	emitStructuredEvent(log.Default(), "info", "runtime_starting", map[string]string{
		"listener_type": "runtime",
	})

	// Open the data dir to check whether it exists.
	if err := runtimeConfig.VerifyDataDirWritable(); err != nil {
		emitStructuredEvent(log.Default(), "error", "persistence_error", map[string]string{
			"listener_type": "runtime",
			"reason":        err.Error(),
		})
		log.Fatalf("Unable to initialize data directory (%v): %v", Args.DataDir, err)
	}
	runtimeState.SetCheck("dataDirectory", "ok")
	dataDir, err := os.Open(Args.DataDir)
	dataDir.Close()

	// Set up logging
	if runtimeConfig.TeamlancerMode {
		logtarget.Default = logtarget.OpenWriters(os.Stdout, os.Stderr)
	} else {
		logtarget.Default, err = logtarget.OpenFile(Args.LogPath, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to open log file (%v): %v", Args.LogPath, err)
			return
		}
	}
	log.SetPrefix("[G] ")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(logtarget.Default)
	log.Printf("Grumble")
	log.Printf("Using data directory: %s", Args.DataDir)

	// Open the blobstore.  If the directory doesn't
	// already exist, create the directory and open
	// the blobstore.
	// The Open method of the blobstore performs simple
	// sanity checking of content of the blob directory,
	// and will return an error if something's amiss.
	blobDir := filepath.Join(Args.DataDir, "blob")
	err = os.Mkdir(blobDir, 0700)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("Unable to create blob directory (%v): %v", blobDir, err)
	}
	blobStore = blobstore.Open(blobDir)

	// Check whether we should regenerate the default global keypair
	// and corresponding certificate.
	// These are used as the default certificate of all virtual servers
	// and the SSH admin console, but can be overridden using the "key"
	// and "cert" arguments to Grumble.
	if shouldManageRuntimeCertificate(runtimeConfig) {
		certFn, keyFn := runtimeCertificatePaths(Args.DataDir)
		certState, err := ensureRuntimeCertificate(Args.DataDir, Args.RegenKeys)
		if err != nil {
			emitStructuredEvent(log.Default(), "error", "persistence_error", map[string]string{
				"listener_type": "raw_tcp",
				"reason":        err.Error(),
			})
			log.Fatal(err)
		}
		if certState == "generated" {
			log.Printf("Generating 4096-bit RSA keypair for self-signed certificate...")
			log.Printf("Certificate output to %v", certFn)
			log.Printf("Private key output to %v", keyFn)
		}
	}

	// Should we import data from a Murmur SQLite file?
	if SQLiteSupport && len(Args.SQLiteDB) > 0 {
		f, err := os.Open(Args.DataDir)
		if err != nil {
			log.Fatalf("Murmur import failed: %s", err.Error())
		}
		defer f.Close()

		names, err := f.Readdirnames(-1)
		if err != nil {
			log.Fatalf("Murmur import failed: %s", err.Error())
		}

		if !Args.CleanUp && len(names) > 0 {
			log.Fatalf("Non-empty datadir. Refusing to import Murmur data.")
		}
		if Args.CleanUp {
			log.Print("Cleaning up existing data directory")
			for _, name := range names {
				if err := os.RemoveAll(filepath.Join(Args.DataDir, name)); err != nil {
					log.Fatalf("Unable to cleanup file: %s", name)
				}
			}
		}

		log.Printf("Importing Murmur data from '%s'", Args.SQLiteDB)
		if err = MurmurImport(Args.SQLiteDB); err != nil {
			log.Fatalf("Murmur import failed: %s", err.Error())
		}

		log.Printf("Import from Murmur SQLite database succeeded.")
		log.Printf("Please restart Grumble to make use of the imported data.")

		return
	}

	// Create the servers directory if it doesn't already
	// exist.
	serversDirPath := filepath.Join(Args.DataDir, "servers")
	err = os.Mkdir(serversDirPath, 0700)
	if err != nil && !os.IsExist(err) {
		log.Fatalf("Unable to create servers directory: %v", err)
	}

	// Read all entries of the servers directory.
	// We need these to load our virtual servers.
	serversDir, err := os.Open(serversDirPath)
	if err != nil {
		log.Fatalf("Unable to open the servers directory: %v", err.Error())
	}
	names, err := serversDir.Readdirnames(-1)
	if err != nil {
		log.Fatalf("Unable to read file from data directory: %v", err.Error())
	}
	// The data dir file descriptor.
	err = serversDir.Close()
	if err != nil {
		log.Fatalf("Unable to close data directory: %v", err.Error())
		return
	}

	// Look through the list of files in the data directory, and
	// load all virtual servers from disk.
	servers = make(map[int64]*Server)
	serverCount := 0
	for _, name := range names {
		if matched, _ := regexp.MatchString("^[0-9]+$", name); matched {
			serverCount++
			log.Printf("Loading server %v", name)
			s, err := NewServerFromFrozen(name)
			if err != nil {
				log.Fatalf("Unable to load server: %v", err.Error())
			}
			err = s.FreezeToFile()
			if err != nil {
				log.Fatalf("Unable to freeze server to disk: %v", err.Error())
			}
			servers[s.Id] = s
		}
	}
	if runtimeConfig.TeamlancerMode && serverCount > 1 {
		log.Fatal("Teamlancer mode requires exactly one active virtual server")
	}

	// If no servers were found, create the default virtual server.
	if len(servers) == 0 {
		s, err := NewServer(1)
		if err != nil {
			log.Fatalf("Couldn't start server: %s", err.Error())
		}

		servers[s.Id] = s
		os.Mkdir(filepath.Join(serversDirPath, fmt.Sprintf("%v", 1)), 0750)
		err = s.FreezeToFile()
		if err != nil {
			log.Fatalf("Unable to freeze newly created server to disk: %v", err.Error())
		}
	}
	if runtimeConfig.TeamlancerMode && len(servers) != 1 {
		log.Fatal("Teamlancer mode requires exactly one active virtual server")
	}
	runtimeState.SetCheck("virtualServer", "ok")

	// Launch the servers we found during launch...
	for _, server := range servers {
		err = server.Start()
		if err != nil {
			emitStructuredEvent(log.Default(), "error", "listener_error", map[string]string{
				"listener_type": "runtime",
				"reason":        err.Error(),
			})
			log.Printf("Unable to start server %v: %v", server.Id, err.Error())
		} else if runtimeConfig.TeamlancerMode {
			runtimeState.MarkReady()
			emitStructuredEvent(log.Default(), "info", "runtime_ready", map[string]string{
				"listener_type": "runtime",
			})
		}
	}

	// If any servers were loaded, launch the signal
	// handler goroutine and sleep...
	if len(servers) > 0 {
		go SignalHandler()
		select {}
	}
}
