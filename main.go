package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"go.bug.st/serial"
)

func SendReadConcentrationCommand(port serial.Port) error {
	_, err := port.Write([]byte{0xFF, 0x01, 0x86, 0x00, 0x00, 0x00, 0x00, 0x00, 0x79})
	return err
}

func CheckResponseChecksum(packet []byte) bool {
	var checksum byte
	for i := range 7 {
		checksum += packet[i]
	}
	checksum = 0xff - checksum
	checksum += 1
	return checksum == packet[7]
}

func DecodeCO2Concentration(packet []byte) float64 {
	concentration := float64(int(packet[1])*255 + int(packet[2]))
	return concentration
}

func ReadCO2Concentration(port serial.Port) (float64, error) {
	port.SetReadTimeout(50 * time.Millisecond)
	for {
		SendReadConcentrationCommand(port)

		p := []byte{0x00}
		n, err := port.Read(p)
		if err != nil {
			return 0, fmt.Errorf("read serial: %w", err)
		}
		if n != 1 {
			log.Printf("timeout: backing off for 1s")
			time.Sleep(time.Second)
			continue
		}
		if p[0] != 0xff {
			continue
		}

		_, err = io.ReadFull(port, p)
		if err != nil {
			return 0, fmt.Errorf("read serial: %w", err)
		}
		if p[0] != 0x86 {
			continue
		}

		buf := make([]byte, 7)
		_, err = io.ReadFull(port, buf)
		if err != nil {
			return 0, fmt.Errorf("read serial: %w", err)
		}
		buf = append([]byte{0x86}, buf...)

		if ok := CheckResponseChecksum(buf); !ok {
			continue
		}
		c := DecodeCO2Concentration(buf)
		switch c {
		case 409, 499, 515:
			log.Printf("device is starting, waiting")
			time.Sleep(time.Second)
			continue
		}
		return c, nil
	}
}

func main() {
	serialPort := "/dev/tty.usbserial-0001"
	readoutInterval := 2 * time.Second
	listenAddr := ":2112"

	mode := &serial.Mode{
		BaudRate: 9600,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
		DataBits: 8,
	}
	port, err := serial.Open(serialPort, mode)
	if err != nil {
		log.Fatal(err)
	}

	CO2Concentration := -1.0
	ticker := time.NewTicker(readoutInterval)
	go func() {
		for {
			<-ticker.C
			c, err := ReadCO2Concentration(port)
			if err != nil {
				log.Printf("reading CO2 concentration: %v", err)
				CO2Concentration = -1
				continue
			}
			log.Printf("c = %v", c)
			CO2Concentration = c
		}
	}()

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if CO2Concentration == -1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `# HELP carbon_dioxide_concentration_ppm CO2 concentration in PPM
# TYPE carbon_dioxide_concentration_ppm gauge
carbon_dioxide_concentration_ppm %f
`, CO2Concentration)
	})

	log.Printf("Starting Prometheus metrics server on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, nil))
}
