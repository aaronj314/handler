package handler

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/graphql-go/graphql"

	"context"
	"fmt"
	"os"
	"io"
	"path"
	"github.com/satori/go.uuid"
)

const (
	ContentTypeJSON           = "application/json"
	ContentTypeGraphQL        = "application/graphql"
	ContentTypeFormURLEncoded = "application/x-www-form-urlencoded"
	ContentTypeMultipartFormData = "multipart/form-data"
)

type Handler struct {
	Schema *graphql.Schema
	pretty   bool
	graphiql bool
}
type RequestOptions struct {
	Query         string                 `json:"query" url:"query" schema:"query"`
	Variables     map[string]interface{} `json:"variables" url:"variables" schema:"variables"`
	OperationName string                 `json:"operationName" url:"operationName" schema:"operationName"`
}

// a workaround for getting`variables` as a JSON string
type requestOptionsCompatibility struct {
	Query         string `json:"query" url:"query" schema:"query"`
	Variables     string `json:"variables" url:"variables" schema:"variables"`
	OperationName string `json:"operationName" url:"operationName" schema:"operationName"`
}

func getFromForm(values url.Values) *RequestOptions {
	query := values.Get("query")
	if query != "" {
		// get variables map
		variables := make(map[string]interface{}, len(values))
		variablesStr := values.Get("variables")
		json.Unmarshal([]byte(variablesStr), &variables)

		return &RequestOptions{
			Query:         query,
			Variables:     variables,
			OperationName: values.Get("operationName"),
		}
	}

	return nil
}

func getFromMultipartForm(r *http.Request) *RequestOptions {
	query := r.PostFormValue("query")
	if query != "" {
		variables := make(map[string]interface{})
		//
		//vars, _, err := r.FormFile("variables")
		//
		//if err != nil {
		//	fmt.Println(err)
		//	return nil
		//}
		//defer vars.Close()
		//
		//buf := new(bytes.Buffer)
		//buf.ReadFrom(vars)
		//err = json.Unmarshal(buf.Bytes(), &variables)
		//
		//if err != nil {
		//	fmt.Println(err)
		//	return nil
		//}

		//input := variables["input"].(map[string]interface{})
		// fieldName := input["fieldName"].(string)
		//
		// variables as text not a file of type json
		variablesStr := r.PostFormValue("variables")

		if err := json.Unmarshal([]byte(variablesStr), &variables); err != nil {
			fmt.Println(err)
		}

		fieldName := variables["input"].(map[string]interface{})["fieldName"]

		file, _, err := r.FormFile(fieldName.(string))

		if err != nil {
			fmt.Println(err)
			return nil
		}

		defer file.Close()

		cwd, _ := os.Getwd()
		tmp := path.Join(cwd, "tmp")
		if _, err := os.Stat(tmp); os.IsNotExist(err) {
			os.Mkdir(tmp, 0766)
		}

		u, _ := uuid.NewV4()

		bufferFile := tmp+"/"+u.String()

		//input["buffer"]=bufferFile
		variables["input"].(map[string]interface{})["buffer"] = bufferFile

		f, err := os.OpenFile(bufferFile, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			fmt.Println(err)
			return nil
		}

		defer f.Close()

		io.Copy(f, file)


		return &RequestOptions{
			Query:         query,
			Variables:     variables,
			OperationName: r.PostFormValue("operationName"),
		}
	}

	return nil
}

// RequestOptions Parses a http.Request into GraphQL request options struct
func NewRequestOptions(r *http.Request) *RequestOptions {
	if reqOpt := getFromForm(r.URL.Query()); reqOpt != nil {
		return reqOpt
	}

	if r.Method != "POST" {
		return &RequestOptions{}
	}

	if r.Body == nil {
		return &RequestOptions{}
	}

	// TODO: improve Content-Type handling
	contentTypeStr := r.Header.Get("Content-Type")
	contentTypeTokens := strings.Split(contentTypeStr, ";")
	contentType := contentTypeTokens[0]

	switch contentType {
	case ContentTypeGraphQL:
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return &RequestOptions{}
		}
		return &RequestOptions{
			Query: string(body),
		}
	case ContentTypeFormURLEncoded:
		if err := r.ParseForm(); err != nil {
			return &RequestOptions{}
		}

		if reqOpt := getFromForm(r.PostForm); reqOpt != nil {
			return reqOpt
		}

		return &RequestOptions{}

	case ContentTypeMultipartFormData:
		if err := r.ParseMultipartForm(4096); err != nil {
			return &RequestOptions{}
		}

		if reqOpt := getFromMultipartForm(r); reqOpt != nil {
			return reqOpt
		}
		return &RequestOptions{}
	case ContentTypeJSON:
		fallthrough
	default:
		var opts RequestOptions
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return &opts
		}
		err = json.Unmarshal(body, &opts)
		if err != nil {
			// Probably `variables` was sent as a string instead of an object.
			// So, we try to be polite and try to parse that as a JSON string
			var optsCompatible requestOptionsCompatibility
			json.Unmarshal(body, &optsCompatible)
			json.Unmarshal([]byte(optsCompatible.Variables), &opts.Variables)
		}
		return &opts
	}
}

// ContextHandler provides an entrypoint into executing graphQL queries with a
// user-provided context.
func (h *Handler) ContextHandler(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// get query
	opts := NewRequestOptions(r)

	// execute graphql query
	params := graphql.Params{
		Schema:         *h.Schema,
		RequestString:  opts.Query,
		VariableValues: opts.Variables,
		OperationName:  opts.OperationName,
		Context:        ctx,
	}
	result := graphql.Do(params)

	if h.graphiql {
		acceptHeader := r.Header.Get("Accept")
		_, raw := r.URL.Query()["raw"]
		if !raw && !strings.Contains(acceptHeader, "application/json") && strings.Contains(acceptHeader, "text/html") {
			renderGraphiQL(w, params)
			return
		}
	}

	// use proper JSON Header
	w.Header().Add("Content-Type", "application/json; charset=utf-8")

	if h.pretty {
		w.WriteHeader(http.StatusOK)
		buff, _ := json.MarshalIndent(result, "", "\t")

		w.Write(buff)
	} else {
		w.WriteHeader(http.StatusOK)
		buff, _ := json.Marshal(result)

		w.Write(buff)
	}
}

// ServeHTTP provides an entrypoint into executing graphQL queries.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.ContextHandler(r.Context(), w, r)
}

type Config struct {
	Schema   *graphql.Schema
	Pretty   bool
	GraphiQL bool
}

func NewConfig() *Config {
	return &Config{
		Schema:   nil,
		Pretty:   true,
		GraphiQL: true,
	}
}

func New(p *Config) *Handler {
	if p == nil {
		p = NewConfig()
	}
	if p.Schema == nil {
		panic("undefined GraphQL schema")
	}

	return &Handler{
		Schema:   p.Schema,
		pretty:   p.Pretty,
		graphiql: p.GraphiQL,
	}
}
