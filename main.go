package main

import (
	"code.google.com/p/go-uuid/uuid"
	"flag"
	"fmt"
	"github.com/ant0ine/go-json-rest/rest"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"time"
)

type Command string

const (
	START_RECORDING Command = "START_RECORDING"
	STOP_RECORDING  Command = "STOP_RECORDING"
	GET_STATUS      Command = "STATUS"
)

type CameraStatus string

const (
	STATUS_RECORDING    CameraStatus = "RECORDING"
	STATUS_NOTRECORDING CameraStatus = "NOT_RECORDING"
)

type StatusPacket struct {
	Status CameraStatus
}

type CommandPacket struct {
	CameraId   string
	Command    Command
	StatusChan chan<- StatusPacket
}

type RecordingEntry struct {
	Process  *os.Process
	Filename string
}

func process(commandChan <-chan CommandPacket, config Configuration) {

	go func() {
		currentProcs := map[string]RecordingEntry{}

		for pkt := range commandChan {
			if url, ok := config.Cameras[pkt.CameraId]; ok {
				if pkt.Command == START_RECORDING {
					if _, ok := currentProcs[pkt.CameraId]; !ok {
						filename := time.Now().UTC().Format("2006-01-02-15-04-05") + "_" + pkt.CameraId + "_" + uuid.New() + ".webm"
						saveLocation := config.SavePath + filename
						options := []string{
							"-e",
							"rtspsrc",
							"location=" + url,
							"!",
							"rtph264depay",
							"!",
							"avdec_h264",
							"!",
							"queue",
							"!",
							"vp8enc",
							"deadline=100",
							"!",
							"webmmux",
							"!",
							"filesink",
							"location=" + saveLocation,
						}

						cmd := exec.Command("gst-launch-1.0", options...)
						if err := cmd.Start(); err == nil {
							currentProcs[pkt.CameraId] = RecordingEntry{
								Process:  cmd.Process,
								Filename: saveLocation,
							}

							if config.Scripts.RecordStart != "" {
								exec.Command(config.Scripts.RecordStart, pkt.CameraId, saveLocation).Start()
							}
						} else {
							fmt.Println(err)
						}
					}
				}

				if pkt.Command == STOP_RECORDING {
					if entry, ok := currentProcs[pkt.CameraId]; ok {
						go func() {
							entry.Process.Signal(os.Interrupt)
							entry.Process.Wait()

							if config.Scripts.RecordEnd != "" {
								exec.Command(config.Scripts.RecordEnd, pkt.CameraId, entry.Filename).Start()
							}
						}()
						delete(currentProcs, pkt.CameraId)
					}
				}

				if pkt.Command == GET_STATUS {
					if _, ok := currentProcs[pkt.CameraId]; ok {
						pkt.StatusChan <- StatusPacket{
							Status: STATUS_RECORDING,
						}
					} else {
						pkt.StatusChan <- StatusPacket{
							Status: STATUS_NOTRECORDING,
						}
					}
					close(pkt.StatusChan)
				}
			}
		}
	}()
}

type Message struct {
	Body string
}

type Configuration struct {
	Cameras  map[string]string
	SavePath string `yaml:"save_path"`
	Scripts  struct {
		RecordStart string `yaml:"record_start"`
		RecordEnd   string `yaml:"record_end"`
	}
}

func main() {
	var configLocation string
	flag.StringVar(&configLocation, "config", "", "config location")
	flag.Parse()

	configBytes, err := ioutil.ReadFile(configLocation)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	config := Configuration{}
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	commandChan := make(chan CommandPacket)

	process(commandChan, config)

	handler := rest.ResourceHandler{}

	err = handler.SetRoutes(
		&rest.Route{"GET", "/cameras", func(w rest.ResponseWriter, req *rest.Request) {
			response := make([]string, len(config.Cameras))
			idx := 0
			for c := range config.Cameras {
				response[idx] = c
				idx++
			}
			w.WriteJson(response)
		}},

		&rest.Route{"GET", "/:camera/:action", func(w rest.ResponseWriter, req *rest.Request) {

			camera := req.PathParam("camera")

			if _, ok := config.Cameras[camera]; !ok {
				rest.Error(w, "camera not recognized", 400)
				return
			}

			action := req.PathParam("action")
			if action == "start" {
				commandChan <- CommandPacket{
					CameraId: camera,
					Command:  START_RECORDING,
				}
				w.WriteJson(&Message{Body: "OK"})
				return
			}

			if action == "stop" {
				commandChan <- CommandPacket{
					CameraId: camera,
					Command:  STOP_RECORDING,
				}
				w.WriteJson(&Message{Body: "OK"})
				return
			}

			if action == "status" {
				statusChan := make(chan StatusPacket)
				commandChan <- CommandPacket{
					CameraId:   camera,
					Command:    GET_STATUS,
					StatusChan: statusChan,
				}

				status := <-statusChan
				w.WriteJson(&status)
				return
			}

			rest.Error(w, "action not recognized", 400)
			return
		}},
	)

	if err == nil {
		http.ListenAndServe(":8080", &handler)
	}
}
