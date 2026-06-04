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
)

const (
	HTTP_CHARGE_PATH = "/charge"
	HTTP_CLIMATE_PATH = "/climate"
)

type Configuration struct {

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
}

type AppData struct {
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
		log.Fatal("Failed to start adb", adbErr)
		os.Exit(5)
	}

	http.HandleFunc(HTTP_CHARGE_PATH, configuration.handleHttpCharge)
	http.HandleFunc(HTTP_CLIMATE_PATH, configuration.handleHttpClimate)

        err := http.ListenAndServe(configuration.API_PORT, nil)
	if err != nil {
                log.Fatal(err)
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
	io.WriteString(w, fmt.Sprintf("{\"status\":\"%s\",\"climater\":%t,\"range\":%d,\"soc\":%d}", value.Status, value.Climater, value.Range, value.Soc))
}

func (config *Configuration) handleHttpCharge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type","application/json; charset=utf-8")
	value, err := config.readAppData()
	if err != nil {
		log.Fatal(err)
		writeBadRequest(w, "Could not read charge value from adb.")
		return
	}
	writeAppDataResponse(w, value)
}

func (config *Configuration) handleHttpClimate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type","application/json; charset=utf-8")
	value, err := config.toggleClimate()
	if err != nil {
		log.Fatal(err)
		writeBadRequest(w, "Could not write climate value to adb.")
		return
	}
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

	shellOutput, err := dev.RunShellCommand(config.CMD_REFRESH)
	if err != nil {
		return nil, err
	}
	time.Sleep(10 * time.Second)
	shellOutput, err = dev.RunShellCommand("uiautomator dump /dev/tty")
	if err != nil {
		return nil, err
	}
	appData := AppData {}
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
	hasError := config.regError.MatchString(shellOutput)
	if hasError {
		appData.Status = "E"
		return &appData, nil
	}
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
	shellOutput, err := dev.RunShellCommand(config.CMD_CLIMATE)
	if err != nil {
		return nil, err
	}
	time.Sleep(1 * time.Second)
	shellOutput, err = dev.RunShellCommand("uiautomator dump /dev/tty")
	if err != nil {
		return nil, err
	}
	appData := AppData {}
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
// evcc does not support error status E?
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