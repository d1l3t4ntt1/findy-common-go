package authn

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"time"

	"github.com/findy-network/findy-grpc/acator"
	"github.com/findy-network/findy-grpc/acator/cose"
	"github.com/golang/glog"
	"github.com/google/uuid"
	"github.com/lainio/err2"
	"github.com/lainio/err2/assert"
	"golang.org/x/net/publicsuffix"
)

type Cmd struct {
	SubCmd   string `json:"sub_cmd"`
	UserName string `json:"user_name"`
	Url      string `json:"url,omitempty"`
	AAGUID   string `json:"aaguid,omitempty"`
	Key      string `json:"key,omitempty"`
	Counter  uint64 `json:"counter,omitempty"`
}

func (ac *Cmd) Validate() (err error) {
	assert.ProductionMode = true
	defer err2.Return(&err)

	assert.NotEmpty(ac.SubCmd, "sub command needed")
	assert.Truef(ac.SubCmd == "register" || ac.SubCmd == "login",
		"wrong sub command: %s: want: register|login", ac.SubCmd)
	assert.NotEmpty(ac.UserName, "user name needed")
	assert.NotEmpty(ac.Url, "connection url cannot be empty")
	assert.NotEmpty(ac.AAGUID, "authenticator ID needed")
	assert.NotEmpty(ac.Key, "master key needed")

	return nil
}

type Result struct {
	SubCmd string `json:"sub_cmd,omitempty"`
	Token  string `json:"token"`
}

func (r Result) String() string {
	d, _ := json.Marshal(r)
	return string(d)
}

func (ac *Cmd) Exec(_ io.Writer) (r Result, err error) {
	defer err2.Annotate("execute authenticator", &err)

	err2.Check(ac.Validate())

	err2.Check(cose.SetMasterKey(ac.Key))
	cmd := cmdModes[ac.SubCmd]
	acator.AAGUID = uuid.Must(uuid.Parse(ac.AAGUID))
	acator.Counter = uint32(ac.Counter)
	name = ac.UserName
	urlStr = ac.Url
	originURL := err2.URL.Try(url.Parse(urlStr))
	acator.Origin = *originURL

	result, err := execute[cmd]()
	err2.Check(err)

	return *result, nil
}

func (ac Cmd) TryReadJSON(r io.Reader) Cmd {
	var newCmd Cmd
	err2.Check(json.NewDecoder(os.Stdin).Decode(&newCmd))
	if newCmd.AAGUID == "" {
		newCmd.AAGUID = ac.AAGUID
	}
	if newCmd.Url == "" {
		newCmd.Url = ac.Url
	}
	if newCmd.Key == "" {
		newCmd.Key = ac.Key
	}
	if newCmd.Counter == 0 {
		newCmd.Counter = ac.Counter
	}
	return newCmd
}

type cmdMode int

const (
	register cmdMode = iota + 1
	login
)

type cmdFunc func() (*Result, error)

var (
	name   string
	urlStr string

	c = setupClient()

	cmdModes = map[string]cmdMode{
		"register": register,
		"login":    login,
	}

	execute = []cmdFunc{
		empty,
		registerUser,
		loginUser,
	}
)

func empty() (*Result, error) {
	msg := "empty command handler called"
	glog.Warningln(msg)
	return nil, errors.New(msg)
}

func registerUser() (result *Result, err error) {
	defer err2.Annotate("register user", &err)

	r := tryHTTPRequest("GET", urlStr+"/register/begin/"+name, nil)
	defer r.Close()

	js := err2.R.Try(acator.Register(r))

	r2 := tryHTTPRequest("POST", urlStr+"/register/finish/"+name, js)
	defer r2.Close()

	b := err2.Bytes.Try(ioutil.ReadAll(r2))
	return &Result{SubCmd: "register", Token: string(b)}, nil
}

func loginUser() (_ *Result, err error) {
	defer err2.Annotate("login user", &err)

	r := tryHTTPRequest("GET", urlStr+"/login/begin/"+name, nil)
	defer r.Close()

	js := err2.R.Try(acator.Login(r))

	r2 := tryHTTPRequest("POST", urlStr+"/login/finish/"+name, js)
	defer r2.Close()

	var result Result
	err2.Check(json.NewDecoder(r2).Decode(&result))

	result.SubCmd = "login"
	return &result, nil
}

func tryHTTPRequest(method, addr string, msg io.Reader) (reader io.ReadCloser) {
	URL := err2.URL.Try(url.Parse(addr))
	request, _ := http.NewRequest(method, URL.String(), msg)

	echoReqToStdout(request)

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", urlStr)
	request.Header.Add("Accept", "*/*")
	request.Header.Add("Cookie", "kviwkdmc83en9csd893j2d298jd8u2c3jd283jcdn2cwc937jd97823jc73h2d67g9d236ch2")

	response := err2.Response.Try(c.Do(request))

	c.Jar.SetCookies(URL, response.Cookies())

	if response.StatusCode != http.StatusOK {
		err2.Check(fmt.Errorf("status code: %v", response.Status))
	}
	echoRespToStdout(response)
	return response.Body
}

func setupClient() (client *http.Client) {
	options := cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	}

	jar, _ := cookiejar.New(&options)

	// Create new http client with predefined options
	client = &http.Client{
		Jar:     jar,
		Timeout: time.Minute * 10,
	}
	return
}

func echoReqToStdout(r *http.Request) {
	if glog.V(5) && r.Body != nil {
		r.Body = &struct {
			io.Reader
			io.Closer
		}{io.TeeReader(r.Body, os.Stdout), r.Body}
	}
}

func echoRespToStdout(r *http.Response) {
	if glog.V(5) && r.Body != nil {
		r.Body = &struct {
			io.Reader
			io.Closer
		}{io.TeeReader(r.Body, os.Stdout), r.Body}
	}
}
