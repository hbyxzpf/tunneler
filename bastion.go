package main

import (
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
	"io"
	"io/ioutil"
	"net"
	"sync"
)

type Bastion struct {
	BastionConfig
	*ssh.Client
	wg    *sync.WaitGroup
	errch chan error
}

func (b *Bastion) Forward(t Tunnel) {
	defer b.wg.Done()
	laddr := net.JoinHostPort(t.LocalHost, t.LocalPort)
	raddr := net.JoinHostPort(t.RemoteHost, t.RemotePort)
	server, err := net.Listen("tcp", laddr)
	if err != nil {
		b.errch <- err
		return
	}
	defer server.Close()
	if t.callback != nil {
		t.callback(server.Addr())
	}

	for {
		lc, err := server.Accept()
		if err != nil {
			b.errch <- err
			continue
		}
		defer lc.Close()

		rc, err := b.Dial("tcp", raddr)
		if err != nil {
			b.errch <- err
			continue
		}
		defer rc.Close()
		go transfer(rc, lc, "remote -> local:", b.errch)
		go transfer(lc, rc, "local -> remote:", b.errch)
	}
}

func (b *Bastion) Up() {
	go handleError(b.errch)
	for _, t := range b.Tunnels {
		b.wg.Add(1)
		go b.Forward(t)
	}
	for _, c := range b.Cascades {
		ch := make(chan net.Addr)
		t := Tunnel{"0.0.0.0", "0", c.Host, c.Port, func(addr net.Addr) { ch <- addr }}
		b.wg.Add(1)
		go b.Forward(t)
		var err error
		c.Host, c.Port, err = net.SplitHostPort((<-ch).String())
		if err != nil {
			b.errch <- err
			continue
		}
		b.wg.Add(1)
		go start(c, b.wg, b.errch)
	}
	b.wg.Wait()
}

func NewBastion(config BastionConfig, errch chan error) (*Bastion, error) {
	signer, err := newSignerFromPath(config.CertPath)
	if err != nil {
		return nil, err
	}
	cc := &ssh.ClientConfig{
		User:            config.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	c, err := ssh.Dial("tcp", config.Host, cc)
	if err != nil {
		return nil, err
	}
	return &Bastion{config, c, new(sync.WaitGroup), errch}, nil
}

func newSignerFromPath(path string) (ssh.Signer, error) {
	privkey, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return ssh.ParsePrivateKey(privkey)
}

func transfer(src, dst net.Conn, label string, errch chan error) {
	_, err := io.Copy(dst, src)
	if err != nil {
		err = errors.Wrap(err, label+err.Error())
		errch <- err
	}
}
