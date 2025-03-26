package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/kardianos/service"
	"github.com/sqweek/dialog"
)

// Config holds the source and destination folder paths.
type Config struct {
	SourceDir string `json:"source_dir"`
	DestDir   string `json:"dest_dir"`
}

var configFile = "config.json"

// readConfig loads configuration from config.json.
func readConfig() (*Config, error) {
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// writeConfig saves the configuration to config.json.
func writeConfig(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(configFile, data, 0644)
}

// Global logger for the service.
var svcLogger service.Logger

// program implements the service.Interface.
type program struct {
	exit   chan struct{}
	config *Config
}

// Start is called when the service is started.
func (p *program) Start(s service.Service) error {
	if svcLogger != nil {
		svcLogger.Info("Service starting...")
	}
	p.exit = make(chan struct{})
	go p.run() // Start folder monitoring in a new goroutine.
	return nil
}

// run contains the main logic for folder monitoring.
func (p *program) run() {
	sourceDir := p.config.SourceDir
	destDir := p.config.DestDir

	// Ensure the destination directory exists.
	if _, err := os.Stat(destDir); os.IsNotExist(err) {
		if err = os.MkdirAll(destDir, os.ModePerm); err != nil {
			if svcLogger != nil {
				svcLogger.Errorf("Error creating destination directory: %v", err)
			}
			return
		}
	}

	// Create a new watcher.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		if svcLogger != nil {
			svcLogger.Errorf("Error creating watcher: %v", err)
		}
		return
	}
	defer watcher.Close()

	// Add the source directory to the watcher.
	if err := watcher.Add(sourceDir); err != nil {
		if svcLogger != nil {
			svcLogger.Errorf("Error adding source directory to watcher: %v", err)
		}
		return
	}

	if svcLogger != nil {
		svcLogger.Infof("Monitoring directory: %s", sourceDir)
	}

	// Main loop to process events.
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// When a new file is created:
			if event.Op&fsnotify.Create == fsnotify.Create {
				if svcLogger != nil {
					svcLogger.Infof("New file detected: %s", event.Name)
				}
				// Check that it is a file (not a directory).
				info, err := os.Stat(event.Name)
				if err != nil {
					if svcLogger != nil {
						svcLogger.Errorf("Error stating file: %v", err)
					}
					continue
				}
				if info.IsDir() {
					if svcLogger != nil {
						svcLogger.Infof("Directory created, skipping: %s", event.Name)
					}
					continue
				}
				// Copy the file to the destination folder.
				destPath := filepath.Join(destDir, filepath.Base(event.Name))
				if err := copyFile(event.Name, destPath); err != nil {
					if svcLogger != nil {
						svcLogger.Errorf("Error copying file: %v", err)
					}
				} else {
					if svcLogger != nil {
						svcLogger.Infof("Copied file %s to %s", event.Name, destPath)
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			if svcLogger != nil {
				svcLogger.Errorf("Watcher error: %v", err)
			}
		case <-p.exit:
			if svcLogger != nil {
				svcLogger.Info("Service stopping...")
			}
			return
		}
	}
}

// Stop is called when the service is stopped.
func (p *program) Stop(s service.Service) error {
	close(p.exit)
	if svcLogger != nil {
		svcLogger.Info("Service stopped")
	}
	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func main() {
	// Define a flag for running the configuration UI.
	configFlag := flag.Bool("config", false, "Run configuration UI to select folders")
	flag.Parse()

	// If -config is provided, show folder selection dialogs.
	if *configFlag {
		src, err := dialog.Directory().Title("Select Source Folder").Browse()
		if err != nil {
			log.Fatalf("Error selecting source folder: %v", err)
		}
		dest, err := dialog.Directory().Title("Select Destination Folder").Browse()
		if err != nil {
			log.Fatalf("Error selecting destination folder: %v", err)
		}
		cfg := &Config{
			SourceDir: src,
			DestDir:   dest,
		}
		err = writeConfig(cfg)
		if err != nil {
			log.Fatalf("Error writing config file: %v", err)
		}
		fmt.Println("Configuration saved successfully to", configFile)
		return
	}

	// Read configuration from file.
	cfg, err := readConfig()
	if err != nil {
		log.Fatalf("Error reading config: %v", err)
	}

	// Set up the Windows service configuration.
	svcConfig := &service.Config{
		Name:        "FolderMonitorService",
		DisplayName: "Folder Monitor Service",
		Description: "Monitors a folder and copies new files to a destination folder.",
	}

	// Create the service.
	prg := &program{
		config: cfg,
	}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println("Error creating service:", err)
		return
	}

	svcLogger, err = s.Logger(nil)
	if err != nil {
		fmt.Println("Error setting up logger:", err)
	}

	// Run the service.
	err = s.Run()
	if err != nil {
		svcLogger.Error(err)
	}
}
