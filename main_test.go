package main

import (
	"bytes"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var ownDockerIp = func() string {
	out, err := exec.Command("docker", "network", "inspect", "bridge",
		"-f", "{{range .IPAM.Config}}{{.Subnet}}{{end}}").Output()
	if err != nil {
		panic(err)
	}

	_, ipnet, err := net.ParseCIDR(string(out[:len(out)-1]))
	if err != nil {
		panic(err)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		panic(err)
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if v, ok := addr.(*net.IPNet); ok {
				if ipnet.Contains(v.IP) {
					return v.IP.String()
				}
			}
		}
	}

	panic("IP not found")
}()

type container struct {
	name   string
	stdout *bytes.Buffer
}

func newContainer(image string, name string, args []string) (*container, error) {
	c := &container{
		name:   name,
		stdout: bytes.NewBuffer(nil),
	}

	exec.Command("docker", "kill", "rtsp-simple-proxy-test-"+name).Run()
	exec.Command("docker", "wait", "rtsp-simple-proxy-test-"+name).Run()

	cmd := []string{"docker", "run", "--name=rtsp-simple-proxy-test-" + name,
		"rtsp-simple-proxy-test-" + image}
	cmd = append(cmd, args...)
	ecmd := exec.Command(cmd[0], cmd[1:]...)

	ecmd.Stdout = c.stdout
	ecmd.Stderr = os.Stderr

	err := ecmd.Start()
	if err != nil {
		return nil, err
	}

	time.Sleep(1 * time.Second)

	return c, nil
}

func (c *container) close() {
	exec.Command("docker", "kill", "rtsp-simple-proxy-test-"+c.name).Run()
	exec.Command("docker", "wait", "rtsp-simple-proxy-test-"+c.name).Run()
	exec.Command("docker", "rm", "rtsp-simple-proxy-test-"+c.name).Run()
}

func (c *container) wait() {
	exec.Command("docker", "wait", "rtsp-simple-proxy-test-"+c.name).Run()
}

func (c *container) ip() string {
	byts, _ := exec.Command("docker", "inspect", "-f",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		"rtsp-simple-proxy-test-"+c.name).Output()
	return string(byts[:len(byts)-1])
}

func TestProtocols(t *testing.T) {
	for _, pair := range [][2]string{
		{"udp", "udp"},
		{"udp", "tcp"},
		{"tcp", "udp"},
		{"tcp", "tcp"},
	} {
		t.Run(pair[0]+"_"+pair[1], func(t *testing.T) {
			cnt1, err := newContainer("rtsp-simple-server", "server", []string{})
			require.NoError(t, err)
			defer cnt1.close()

			time.Sleep(1 * time.Second)

			cnt2, err := newContainer("ffmpeg", "source", []string{
				"-hide_banner",
				"-loglevel", "panic",
				"-re",
				"-stream_loop", "-1",
				"-i", "/emptyvideo.ts",
				"-c", "copy",
				"-f", "rtsp",
				"-rtsp_transport", "udp",
				"rtsp://" + cnt1.ip() + ":8554/teststream",
			})
			require.NoError(t, err)
			defer cnt2.close()

			time.Sleep(1 * time.Second)

			ioutil.WriteFile("conf.yml", []byte("\n"+
				"server:\n"+
				"  protocols: [ "+pair[1]+" ]\n"+
				"  rtspPort: 8555\n"+
				"\n"+
				"streams:\n"+
				"  testproxy:\n"+
				"    url: rtsp://"+cnt1.ip()+":8554/teststream\n"+
				"    protocol: "+pair[0]+"\n"),
				0644)

			p, err := newProgram([]string{})
			require.NoError(t, err)
			defer p.close()

			time.Sleep(1 * time.Second)

			cnt3, err := newContainer("ffmpeg", "dest", []string{
				"-hide_banner",
				"-loglevel", "panic",
				"-rtsp_transport", pair[1],
				"-i", "rtsp://" + ownDockerIp + ":8555/testproxy",
				"-vframes", "1",
				"-f", "image2",
				"-y", "/dev/null",
			})
			require.NoError(t, err)
			defer cnt3.close()

			cnt3.wait()

			require.Equal(t, "all right\n", string(cnt3.stdout.Bytes()))
		})
	}
}

func TestStreamAuth(t *testing.T) {
	cnt1, err := newContainer("rtsp-simple-server", "server", []string{
		"--read-user=testuser",
		"--read-pass=testpass",
	})
	require.NoError(t, err)
	defer cnt1.close()

	time.Sleep(1 * time.Second)

	cnt2, err := newContainer("ffmpeg", "source", []string{
		"-hide_banner",
		"-loglevel", "panic",
		"-re",
		"-stream_loop", "-1",
		"-i", "/emptyvideo.ts",
		"-c", "copy",
		"-f", "rtsp",
		"-rtsp_transport", "udp",
		"rtsp://" + cnt1.ip() + ":8554/teststream",
	})
	require.NoError(t, err)
	defer cnt2.close()

	time.Sleep(1 * time.Second)

	ioutil.WriteFile("conf.yml", []byte("\n"+
		"server:\n"+
		"  protocols: [ udp ]\n"+
		"  rtspPort: 8555\n"+
		"\n"+
		"streams:\n"+
		"  testproxy:\n"+
		"    url: rtsp://testuser:testpass@"+cnt1.ip()+":8554/teststream\n"+
		"    protocol: udp\n"),
		0644)

	p, err := newProgram([]string{})
	require.NoError(t, err)
	defer p.close()

	time.Sleep(1 * time.Second)

	cnt3, err := newContainer("ffmpeg", "dest", []string{
		"-hide_banner",
		"-loglevel", "panic",
		"-rtsp_transport", "udp",
		"-i", "rtsp://" + ownDockerIp + ":8555/testproxy",
		"-vframes", "1",
		"-f", "image2",
		"-y", "/dev/null",
	})
	require.NoError(t, err)
	defer cnt3.close()

	cnt3.wait()

	require.Equal(t, "all right\n", string(cnt3.stdout.Bytes()))
}

func TestServerAuth(t *testing.T) {
	cnt1, err := newContainer("rtsp-simple-server", "server", []string{})
	require.NoError(t, err)
	defer cnt1.close()

	time.Sleep(1 * time.Second)

	cnt2, err := newContainer("ffmpeg", "source", []string{
		"-hide_banner",
		"-loglevel", "panic",
		"-re",
		"-stream_loop", "-1",
		"-i", "/emptyvideo.ts",
		"-c", "copy",
		"-f", "rtsp",
		"-rtsp_transport", "udp",
		"rtsp://" + cnt1.ip() + ":8554/teststream",
	})
	require.NoError(t, err)
	defer cnt2.close()

	time.Sleep(1 * time.Second)

	ioutil.WriteFile("conf.yml", []byte("\n"+
		"server:\n"+
		"  protocols: [ udp ]\n"+
		"  rtspPort: 8555\n"+
		"  readUser: testuser\n"+
		"  readPass: testpass\n"+
		"\n"+
		"streams:\n"+
		"  testproxy:\n"+
		"    url: rtsp://"+cnt1.ip()+":8554/teststream\n"+
		"    protocol: udp\n"),
		0644)

	p, err := newProgram([]string{})
	require.NoError(t, err)
	defer p.close()

	time.Sleep(1 * time.Second)

	cnt3, err := newContainer("ffmpeg", "dest", []string{
		"-hide_banner",
		"-loglevel", "panic",
		"-rtsp_transport", "udp",
		"-i", "rtsp://testuser:testpass@" + ownDockerIp + ":8555/testproxy",
		"-vframes", "1",
		"-f", "image2",
		"-y", "/dev/null",
	})
	require.NoError(t, err)
	defer cnt3.close()

	cnt3.wait()

	require.Equal(t, "all right\n", string(cnt3.stdout.Bytes()))
}
