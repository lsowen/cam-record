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
)

type CommandPacket struct {
	CameraId string
	Command  Command
}

func process(commandChan <-chan CommandPacket, config Configuration) {

	go func() {
		currentProcs := map[string]*os.Process{}

		for pkt := range commandChan {
			if url, ok := config.Cameras[pkt.CameraId]; ok {
				if pkt.Command == START_RECORDING {
					if _, ok := currentProcs[pkt.CameraId]; !ok {

						savePath := "/recorder/"
						filename := time.Now().UTC().Format("2006-01-02-15-04-05") + "_" + pkt.CameraId + "_" + uuid.New() + ".webm"
						saveLocation := savePath + filename
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
							currentProcs[pkt.CameraId] = cmd.Process
						} else {
							fmt.Println(err)
						}
					}
				}

				if pkt.Command == STOP_RECORDING {
					if proc, ok := currentProcs[pkt.CameraId]; ok {
						//TODO: handle errors?
						go func() {
							proc.Signal(os.Interrupt)
							proc.Wait()
						}()
						delete(currentProcs, pkt.CameraId)
					}
				}
			}
		}
	}()
}

type Message struct {
	Body string
}

type Configuration struct {
	Cameras map[string]string
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

			rest.Error(w, "action not recognized", 400)
			return
		}},
	)

	if err == nil {
		http.ListenAndServe(":8080", &handler)
	}
}
