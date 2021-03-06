package platform

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bravetools/bravetools/shared"
	"github.com/mitchellh/go-ps"
)

type (
	// Multipass type defines local dev VM
	Multipass struct {
		Settings HostSettings
	}
)

// NewMultipass constructor
func NewMultipass(settings HostSettings) *Multipass {
	return &Multipass{
		Settings: settings,
	}
}

// checkMultipass checks if Multipass is running
func checkMultipass() (bool, error) {

	ps, err := ps.Processes()
	if err != nil {
		return false, err
	}

	found := false
	for _, p := range ps {
		if strings.Contains(p.Executable(), "multipass") {
			found = true
			break
		}
	}

	if !found {
		return false, errors.New("Install multipass")
	}

	return true, nil
}

// BraveBackendInit creates a new instance of BraveAI host
func (vm Multipass) BraveBackendInit() error {

	_, err := checkMultipass()
	if err != nil {
		return err
	}

	err = shared.ExecCommand("multipass",
		"launch",
		"--cpus",
		vm.Settings.BackendSettings.Resources.CPU,
		"--disk",
		vm.Settings.BackendSettings.Resources.HD,
		"--mem",
		vm.Settings.BackendSettings.Resources.RAM,
		"--name",
		vm.Settings.BackendSettings.Resources.Name,
		vm.Settings.BackendSettings.Resources.OS)
	if err != nil {
		return errors.New("Failed to create workspace: " + err.Error())
	}

	time.Sleep(10 * time.Second)

	usr, err := user.Current()
	if err != nil {
		return errors.New("Unable to fetch current user information: " + err.Error())
	}

	err = shared.ExecCommand("multipass",
		"mount",
		filepath.Join(usr.HomeDir, ".bravetools"),
		vm.Settings.Name+":/home/ubuntu/.bravetools/")

	if err != nil {
		return errors.New("Unable to mount local volumes to multipass: " + err.Error())
	}

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"sudo",
		"apt",
		"update")

	if err != nil {
		return errors.New("Failed to update workspace: " + err.Error())
	}

	shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"sudo",
		"apt",
		"remove",
		"-y",
		"lxd")
	shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"sudo",
		"apt",
		"autoremove",
		"-y")
	shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"sudo",
		"apt",
		"purge")

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"sudo",
		"snap",
		"install",
		"--stable",
		"lxd")

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"sudo",
		"usermod",
		"-aG",
		"lxd",
		"ubuntu")

	if err != nil {
		return errors.New("Failed to install packages in workspace: " + err.Error())
	}

	fmt.Println("Installing required software ...")
	time.Sleep(10 * time.Second)

	timestamp := time.Now()
	storagePoolName := vm.Settings.StoragePool.Name + "-" + timestamp.Format("20060102150405")
	vm.Settings.StoragePool.Name = storagePoolName

	err = UpdateBraveSettings(vm.Settings)
	if err != nil {
		return errors.New("Failed update settings" + err.Error())
	}

	var lxdInit = `cat <<EOF | sudo lxd init --preseed
pools:
- name: ` + vm.Settings.StoragePool.Name + "\n" +
		`  driver: zfs
networks:
- name: lxdbr0
  type: bridge
  config:` + "\n" +
		"    ipv4.address: " + vm.Settings.Network.Bridge + "/24 \n" +
		`    ipv4.nat: true
    ipv6.address: none
profiles:
- name: ` + vm.Settings.Profile + "\n" +
		`  devices:
    root:
      path: /
      pool: ` + vm.Settings.StoragePool.Name + "\n" +
		`      type: disk
    eth0:
      nictype: bridged
      parent: lxdbr0
      type: nic
EOF`

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		shared.SnapLXC,
		"profile",
		"create",
		vm.Settings.Profile)
	if err != nil {
		return errors.New("Failed to create LXD profile: " + err.Error())
	}

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		shared.SnapLXC,
		"storage",
		"create",
		vm.Settings.StoragePool.Name,
		vm.Settings.StoragePool.Type,
		"size="+vm.Settings.StoragePool.Size)
	if err != nil {
		return errors.New("Failed to create storage pool: " + err.Error())
	}

	shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		shared.SnapLXC,
		"profile",
		"device",
		"add",
		vm.Settings.Profile,
		"root",
		"disk",
		"path=/",
		"pool="+vm.Settings.StoragePool.Name)

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		"bash",
		"-c",
		lxdInit)
	if err != nil {
		return errors.New("Failed to initiate workspace: " + err.Error())
	}

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		shared.SnapLXC,
		"config",
		"set",
		"core.https_address",
		"[::]:8443")
	if err != nil {
		return errors.New("Error connecting to workspace: " + err.Error())
	}

	err = shared.ExecCommand("multipass",
		"exec",
		vm.Settings.Name,
		"--",
		shared.SnapLXC,
		"config",
		"set",
		"core.trust_password",
		vm.Settings.Trust)
	if err != nil {
		return errors.New("Error setting workspace security: " + err.Error())
	}

	vm.Settings.Status = "active"
	err = UpdateBraveSettings(vm.Settings)
	if err != nil {
		return err
	}
	return nil

}

// BraveHostDelete removes BraveAI host
func (vm Multipass) BraveHostDelete() error {

	err := shared.ExecCommand("multipass", "delete", vm.Settings.Name)
	if err != nil {
		return err
	}
	err = shared.ExecCommand("multipass", "purge")
	if err != nil {
		return err
	}

	return nil
}

// Info shows all VMs and their state
func (vm Multipass) Info() (Info, error) {
	backendInfo := Info{}
	_, err := checkMultipass()
	if err != nil {
		return backendInfo, errors.New("Cannot find backend service")
	}

	out, err := exec.Command("multipass", "info", vm.Settings.Name).Output()
	if err != nil {
		return backendInfo, errors.New("Error starting workspace")
	}

	info := strings.Split(string(out), "\n")
	for _, data := range info {
		d := strings.Split(data, ":")
		key := strings.TrimSpace(d[0])
		switch key {
		case "Name":
			backendInfo.Name = strings.TrimSpace(d[1])
		case "State":
			backendInfo.State = strings.TrimSpace(d[1])
		case "IPv4":
			backendInfo.IPv4 = strings.TrimSpace(d[1])
		case "Release":
			backendInfo.Release = strings.TrimSpace(d[1])
		case "Image hash":
			backendInfo.ImageHash = strings.TrimSpace(d[1])
		case "Load":
			backendInfo.Load = strings.TrimSpace(d[1])
		}
	}

	if backendInfo.State == "Running" {
		cmd := shared.SnapLXC + " storage info " + vm.Settings.StoragePool.Name + " --bytes"
		storageInfo, err := shared.ExecCommandWReturn("multipass",
			"exec",
			vm.Settings.Name,
			"--",
			"bash", "-c",
			cmd)

		if err != nil {
			return backendInfo, errors.New("Unable to access host disk usage")
		}

		scanner := bufio.NewScanner(strings.NewReader(storageInfo))
		var totalDisk string
		var usedDisk string

		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.Split(line, ": ")
			if len(parts) > 1 {
				switch parts[0] {
				case "  space used":
					usedDisk = parts[1]

				case "  total space":
					totalDisk = parts[1]
				}
			}

		}

		usedDisk = usedDisk[1 : len(usedDisk)-1]
		totalDisk = totalDisk[1 : len(totalDisk)-1]
		usedDiskInt, err := strconv.ParseInt(usedDisk, 0, 64)
		totalDiskInt, err := strconv.ParseInt(totalDisk, 0, 64)

		usedDisk = shared.FormatByteCountSI(usedDiskInt)
		totalDisk = shared.FormatByteCountSI(totalDiskInt)

		backendInfo.Disk = []string{usedDisk, totalDisk}

		totalMemCmd := "cat /proc/meminfo | grep MemTotal | awk '{print $2}'"
		availableMemCmd := "cat /proc/meminfo | grep MemAvailable | awk '{print $2}'"

		totalMem, err := shared.ExecCommandWReturn("multipass",
			"exec",
			vm.Settings.Name,
			"--",
			"bash", "-c", totalMemCmd)
		if err != nil {
			return backendInfo, errors.New("Cannot assess total RAM count")
		}

		totalMem = strings.Split(strings.TrimSpace(strings.Split(totalMem, ":")[1]), " ")[0]

		availableMem, err := shared.ExecCommandWReturn("multipass",
			"exec",
			vm.Settings.Name,
			"--",
			"bash", "-c", availableMemCmd)

		if err != nil {
			return backendInfo, errors.New("Cannot assess available RAM count")
		}

		availableMem = strings.Split(strings.TrimSpace(strings.Split(availableMem, ":")[1]), " ")[0]

		totalMemInt, err := strconv.Atoi(totalMem)
		availableMemInt, err := strconv.Atoi(availableMem)
		usedMemInt := totalMemInt - availableMemInt

		totalMem = shared.FormatByteCountSI(int64(totalMemInt * 1000))
		usedMem := shared.FormatByteCountSI(int64(usedMemInt * 1000))

		backendInfo.Memory = []string{usedMem, totalMem}

		cpuCount := "grep -c ^processor /proc/cpuinfo"
		cpu, err := shared.ExecCommandWReturn("multipass",
			"exec",
			vm.Settings.Name,
			"--",
			"bash",
			"-c",
			cpuCount)

		if err != nil {
			return backendInfo, errors.New("Cannot assess CPU count")
		}

		backendInfo.CPU = cpu
	} else {
		backendInfo.Memory = []string{"Unknown", "Unknown"}
		backendInfo.Disk = []string{"Unknown", "Unknown"}
		backendInfo.CPU = "Unknown"
	}

	return backendInfo, nil
}
