package moby

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

var linuxkitYaml = map[string]string{"mkimage": `
kernel:
  image: linuxkit/kernel:4.9.39
  cmdline: "console=ttyS0"
init:
  - linuxkit/init:9250948d0de494df8a811edb3242b4584057cfe4
  - linuxkit/runc:abc3f292653e64a2fd488e9675ace19a55ec7023
onboot:
  - name: mkimage
    image: linuxkit/mkimage:e439d6108466186948ca7ea2a293fc6c1d1183fa
  - name: poweroff
    image: linuxkit/poweroff:bccfe1cb04fc7bb9f03613d2314f38abd2620f29
trust:
  org:
    - linuxkit
`}

func imageFilename(name string) string {
	yaml := linuxkitYaml[name]
	hash := sha256.Sum256([]byte(yaml))
	return filepath.Join(MobyDir, "linuxkit", name+"-"+fmt.Sprintf("%x", hash))
}

func ensureLinuxkitImage(name string) error {
	filename := imageFilename(name)
	_, err1 := os.Stat(filename + "-kernel")
	_, err2 := os.Stat(filename + "-initrd.img")
	_, err3 := os.Stat(filename + "-cmdline")
	if err1 == nil && err2 == nil && err3 == nil {
		return nil
	}
	err := os.MkdirAll(filepath.Join(MobyDir, "linuxkit"), 0755)
	if err != nil {
		return err
	}
	// TODO clean up old files
	log.Infof("Building LinuxKit image %s to generate output formats", name)

	yaml := linuxkitYaml[name]

	m, err := NewConfig([]byte(yaml))
	if err != nil {
		return err
	}
	// TODO pass through --pull to here
	buf := new(bytes.Buffer)
	Build(m, buf, false, "")
	image := buf.Bytes()
	kernel, initrd, cmdline, err := tarToInitrd(image)
	if err != nil {
		return fmt.Errorf("Error converting to initrd: %v", err)
	}
	return writeKernelInitrd(filename, kernel, initrd, cmdline)
}

func writeKernelInitrd(filename string, kernel []byte, initrd []byte, cmdline string) error {
	err := ioutil.WriteFile(filename+"-kernel", kernel, 0600)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filename+"-initrd.img", initrd, 0600)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename+"-cmdline", []byte(cmdline), 0600)
}

func outputLinuxKit(format string, filename string, kernel []byte, initrd []byte, cmdline string, size int) error {
	log.Debugf("output linuxkit generated img: %s %s size %d", format, filename, size)

	tmp, err := ioutil.TempDir(filepath.Join(MobyDir, "tmp"), "moby")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	buf, err := tarInitrdKernel(kernel, initrd, cmdline)
	if err != nil {
		return err
	}

	tardisk := filepath.Join(tmp, "tardisk")
	f, err := os.Create(tardisk)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, buf)
	if err != nil {
		return err
	}
	err = f.Close()
	if err != nil {
		return err
	}

	sizeString := fmt.Sprintf("%dM", size)
	_ = os.Remove(filename)
	_, err = os.Stat(filename)
	if err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("Cannot remove existing file [%s]", filename)
	}
	linuxkit, err := exec.LookPath("linuxkit")
	if err != nil {
		return fmt.Errorf("Cannot find linuxkit executable, needed to build %s output type: %v", format, err)
	}
	commandLine := []string{
		"-q", "run", "qemu",
		"-disk", fmt.Sprintf("%s,size=%s,format=%s", filename, sizeString, format),
		"-disk", fmt.Sprintf("%s,format=raw", tardisk),
		"-kernel", imageFilename("mkimage"),
	}
	log.Debugf("run %s: %v", linuxkit, commandLine)
	cmd := exec.Command(linuxkit, commandLine...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
