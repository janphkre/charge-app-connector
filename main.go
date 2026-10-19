package main

import (
	"github.com/electricbubble/gadb"
        "log"
	"fmt"
	"time"
        "net/http"
	"errors"
	"strconv"
	"os"
	"os/exec"
	"io"
	"regexp"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	HTTP_CHARGE_PATH = "/charge"
	HTTP_CLIMATE_PATH = "/climate"
	HTTP_PROBE_PATH = "/probe"
	CACHE_TTL = 14 * time.Minute
)

type Configuration struct {

	regRecent *regexp.Regexp
	regSoc *regexp.Regexp
	regRange *regexp.Regexp
	regClimate *regexp.Regexp
	regError *regexp.Regexp
	regCharge *regexp.Regexp
	regConnected *regexp.Regexp

//ENVIRONMENT:
        API_PORT string
        CMD_REFRESH string
        CMD_CLIMATE string
	ADB_URL string

	// cacheMu also serializes adb access, so concurrent requests don't interfere on the device.
	cacheMu sync.Mutex
	cached *AppData

	// latestMu guards latest, a copy of cached that can be read without waiting for adb.
	latestMu sync.RWMutex
	latest *AppData
}

func (config *Configuration) setCached(value *AppData) {
	config.cached = value
	config.latestMu.Lock()
	config.latest = value
	config.latestMu.Unlock()
}

// Latest returns the most recent app data without any adb traffic, or nil if none was read yet.
func (config *Configuration) Latest() *AppData {
	config.latestMu.RLock()
	defer config.latestMu.RUnlock()
	return config.latest
}

type AppData struct {
	CapturedAt time.Time
	IsRecent bool
	Status string
	Climater bool
	Range int
	Soc int
}

func main() {
	log.Println("Starting car connector")
	configErr, configuration := getConfig()
	if nil != configErr {
		log.Println("Failed to load configuration from env!", configErr)
		os.Exit(3)
	}

	cmd := exec.Command("adb", "start-server")
	adbErr := cmd.Run()
	if adbErr != nil {
		log.Println("Failed to start adb", adbErr)
		os.Exit(5)
	}

	cmd = exec.Command("adb", "connect", configuration.ADB_URL)
	adbErr = cmd.Run()
	if adbErr != nil {
		log.Println("Failed to connect to emulator", adbErr)
		os.Exit(6)
	}

	http.HandleFunc(HTTP_CHARGE_PATH, configuration.handleHttpCharge)
	http.HandleFunc(HTTP_CLIMATE_PATH, configuration.handleHttpClimate)

	registry := prometheus.NewRegistry()
	registry.MustRegister(newAppCollector(configuration))
	http.Handle(HTTP_PROBE_PATH, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

        err := http.ListenAndServe(configuration.API_PORT, nil)
	if err != nil {
                log.Println(err)
		os.Exit(4)
        }
}

func getConfig() (error, *Configuration) {
	configuration := Configuration{}
	var err error
	err, configuration.API_PORT = getEnv("API_PORT")
	if err != nil {
		return err, nil
	}
	err, configuration.CMD_REFRESH = getEnv("CMD_REFRESH")
	if err != nil {
		return err, nil
	}
	err, configuration.CMD_CLIMATE = getEnv("CMD_CLIMATE")
	if err != nil {
		return err, nil
	}

	err, regRecentString := getEnv("REG_RECENT")
	if err != nil {
		return err, nil
	}
	err, regSocString := getEnv("REG_SOC")
	if err != nil {
		return err, nil
	}
	err, regRangeString := getEnv("REG_RANGE")
	if err != nil {
		return err, nil
	}
	err, regClimateString := getEnv("REG_CLIMATE")
	if err != nil {
		return err, nil
	}
	err, regErrorString := getEnv("REG_ERROR")
	if err != nil {
		return err, nil
	}
	err, regChargeString := getEnv("REG_CHARGE")
	if err != nil {
		return err, nil
	}
	err, regConnectedString := getEnv("REG_CONNECTED")
	if err != nil {
		return err, nil
	}

	configuration.regRecent = regexp.MustCompile(regRecentString)
	configuration.regSoc = regexp.MustCompile(regSocString)
	configuration.regRange = regexp.MustCompile(regRangeString)
	configuration.regClimate = regexp.MustCompile(regClimateString)
	configuration.regError = regexp.MustCompile(regErrorString)
	configuration.regCharge = regexp.MustCompile(regChargeString)
	configuration.regConnected = regexp.MustCompile(regConnectedString)
	return nil, &configuration
}

func getEnv(key string) (error, string) {
	result, hasValue := os.LookupEnv(key)
	if !hasValue {
		return errors.New(fmt.Sprintf("Missing environment value %s", key)), ""
	}
	return nil, result
}

func writeBadRequest(w http.ResponseWriter, errorMsg string) {
	w.WriteHeader(http.StatusBadRequest)
	io.WriteString(w, fmt.Sprintf("{\"error\":\"%s\"}", errorMsg))
}

func writeAppDataResponse(w http.ResponseWriter, value *AppData) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, fmt.Sprintf("{\"recent\":%t,\"status\":\"%s\",\"climater\":%t,\"range\":%d,\"soc\":%d,\"timestamp\":\"%s\"}", value.IsRecent, value.Status, value.Climater, value.Range, value.Soc, value.CapturedAt.UTC().Format(time.RFC3339)))
}

func (config *Configuration) handleHttpCharge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type","application/json; charset=utf-8")
	config.cacheMu.Lock()
	defer config.cacheMu.Unlock()
	if config.cached != nil && time.Since(config.cached.CapturedAt) < CACHE_TTL {
		writeAppDataResponse(w, config.cached)
		return
	}
	value, err := config.readAppData()
	if err != nil {
		log.Println(err)
		writeBadRequest(w, "Could not read charge value from adb.")
		return
	}
	config.setCached(value)
	writeAppDataResponse(w, value)
}

// handleHttpClimate always toggles the climate, but refreshes the cache with the result.
func (config *Configuration) handleHttpClimate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type","application/json; charset=utf-8")
	config.cacheMu.Lock()
	defer config.cacheMu.Unlock()
	value, err := config.toggleClimate()
	if err != nil {
		log.Println(err)
		writeBadRequest(w, "Could not write climate value to adb.")
		return
	}
	config.setCached(value)
	writeAppDataResponse(w, value)
}

func connectAdb() (*gadb.Device, error) {
	adbClient, err := gadb.NewClient()
	if err != nil {
		return nil, err
	}

	devices, err := adbClient.DeviceList()
	if err != nil {
		return nil, err
	}

	if len(devices) == 0 {
		return nil, errors.New("No device connected!")
	}
	device := devices[0]
	return &device, nil
}

func (config *Configuration) readAppData() (*AppData, error) {
	dev, err := connectAdb()
	if err != nil {
		return nil, err
	}

	_, err = dev.RunShellCommand(config.CMD_REFRESH)
	if err != nil {
		return nil, err
	}
	time.Sleep(10 * time.Second)
	return config.dumpAppData(dev)
}

func (config *Configuration) dumpAppData(dev *gadb.Device) (*AppData, error) {
	shellOutput, err := dev.RunShellCommand("uiautomator dump /dev/tty")
	if err != nil {
		return nil, err
	}
	appData := AppData { CapturedAt: time.Now() }
	IsRecent := config.regRecent.MatchString(shellOutput)
	appData.IsRecent = IsRecent
	socMatches := config.regSoc.FindStringSubmatch(shellOutput)
	if len(socMatches) > 0 {
		appData.Soc, err = strconv.Atoi(socMatches[1])
	}
	rangeMatches := config.regRange.FindStringSubmatch(shellOutput)
	if len(rangeMatches) > 0 {
		appData.Range, err = strconv.Atoi(rangeMatches[1])
	}
	climateActive := config.regClimate.MatchString(shellOutput)
	appData.Climater = climateActive
//	hasError := config.regError.MatchString(shellOutput)
//	if hasError {
//		appData.Status = "E"
//		return &appData, nil
//	}
	isCharging := config.regCharge.MatchString(shellOutput)
	if isCharging {
		appData.Status = "C"
		return &appData, nil
	}
	isConnected := config.regConnected.MatchString(shellOutput)
	if isConnected {
		appData.Status = "B"
		return &appData, nil
	}
	appData.Status = "A"
	return &appData, nil
}


func (config *Configuration) toggleClimate() (*AppData, error) {
	dev, err := connectAdb()
	if err != nil {
		return nil, err
	}
	_, err = dev.RunShellCommand(config.CMD_CLIMATE)
	if err != nil {
		return nil, err
	}
	time.Sleep(1 * time.Second)
	return config.dumpAppData(dev)
}