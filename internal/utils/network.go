package utils

import (
	"net"
	"os"
)

// GetInstanceIP retrieves the IPv4 address of the current machine's hostname and returns it as a string.
func GetInstanceIP() (string, error) {
	var instanceIP string
	// Get the actual instance IP
	hostname, _ := os.Hostname()
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return instanceIP, err
	}
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			instanceIP = ipv4.String()
			break
		}
	}
	return instanceIP, nil
}
