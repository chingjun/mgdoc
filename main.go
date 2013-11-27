package main

import (
    "fmt"
    "path"
    "time"
    "os"
    "net/http"
    "html/template"
    "io"
    "io/ioutil"
    "bytes"

    "github.com/russross/blackfriday"
    "launchpad.net/goyaml"
)

func markdown(input []byte) (content, toc []byte) {
    htmlFlags := 0
    htmlFlags |= blackfriday.HTML_USE_XHTML
    htmlFlags |= blackfriday.HTML_USE_SMARTYPANTS
    htmlFlags |= blackfriday.HTML_SMARTYPANTS_FRACTIONS
    htmlFlags |= blackfriday.HTML_SMARTYPANTS_LATEX_DASHES
    htmlFlags |= blackfriday.HTML_SKIP_SCRIPT
    htmlFlags |= blackfriday.HTML_TOC
    renderer := blackfriday.HtmlRenderer(htmlFlags, "", "")

    // set up the parser
    extensions := 0
    extensions |= blackfriday.EXTENSION_NO_INTRA_EMPHASIS
    extensions |= blackfriday.EXTENSION_TABLES
    extensions |= blackfriday.EXTENSION_FENCED_CODE
    extensions |= blackfriday.EXTENSION_AUTOLINK
    extensions |= blackfriday.EXTENSION_STRIKETHROUGH
    extensions |= blackfriday.EXTENSION_SPACE_HEADERS

    result := blackfriday.Markdown(input, renderer, extensions)
    endIndex := bytes.Index(result, []byte("</nav>"))
    toc = result[5:endIndex]
    content = result[endIndex+6:]
    return
}

var config map[string] interface{}
var configModTime time.Time
func loadConfig(filename string) (map[string] interface{}, error) {
    configFileContent, err := ioutil.ReadFile(filename)
    if err != nil {
        return nil, err
    }

    config := make(map[string] interface{})
    err = goyaml.Unmarshal(configFileContent, config)
    if err != nil {
        return nil, err
    }
    return config, nil
}

func checkConfigChange(filename string, oriModTime time.Time) (bool, time.Time) {
    mtime, err := getMtime(filename)
    if err != nil {
        return false, time.Time{}
    }
    if !mtime.After(oriModTime) {
        return false, time.Time{}
    }
    return true, mtime
}

func checkConfLoop(filename string) {
    for {
        if changed, mtime := checkConfigChange(filename, configModTime); changed {
            conf, err := loadConfig(filename)
            if err == nil {
                config = conf
                configModTime = mtime
                fmt.Println("conf file updated")
            }
        }
        time.Sleep(5 * time.Second)
    }
}

type PageData struct {
    Config map[string] interface{}
    Title template.HTML
    Content template.HTML
    Toc template.HTML
    Page map[string] interface{}
    SourceLink string
    EditLink string
}

type templateMapValue struct {
    template *template.Template
    mtime time.Time
}
var templateMap map[string] templateMapValue

func getMtime(filename string) (time.Time, error) {
    fileInfo, err := os.Lstat(filename)
    if err != nil {
        return time.Time{}, err
    } else {
        return fileInfo.ModTime(), nil
    }
}

func getTemplate(name string) (*template.Template, error) {
    if templateMap == nil {
        templateMap = make(map[string] templateMapValue)
    }

    filename := fmt.Sprintf("_template/%s.html", name)
    val, ok := templateMap[name]
    mtime, err := getMtime(filename)
    if err != nil {
        return nil, err
    }

    if ok && !val.mtime.Before(mtime) {
        return val.template, nil
    } else {
        tmpl, err := template.ParseFiles(filename)
        if err != nil {
            return nil, err
        }
        templateMap[name] = templateMapValue {
            template: tmpl,
            mtime: mtime,
        }
        return tmpl, nil
    }
}

func sendMarkdown(w io.Writer, url, filename string) error {
    filecontent, err := ioutil.ReadFile(filename)

    if err != nil {
        fmt.Println(err)
        return err
    }

    yamlEndIndex := bytes.Index(filecontent, []byte("\n---"))

    yamlcontent := filecontent[:yamlEndIndex]
    yaml := make(map[string]interface{})
    err = goyaml.Unmarshal(yamlcontent, yaml)

    title, ok := yaml["Title"].(string)
    if !ok {
        title = ""
    }

    templateFile, ok := yaml["Template"].(string)
    if !ok {
        templateFile = "default"
    }

    mdcontent := filecontent[yamlEndIndex+4:]
    content, toc := markdown(mdcontent)

    data := PageData {
        Config: config,
        Title: template.HTML(title),
        Content: template.HTML(content),
        Toc: template.HTML(toc),
        Page: yaml,
        SourceLink: url + ".md",
        EditLink: "?edit=1",
    }

    tmpl, err := getTemplate(templateFile)
    err = tmpl.Execute(w, data)
    if err != nil {
        fmt.Println(err)
        return err
    }

    return nil
}

func handler(w http.ResponseWriter, r *http.Request) {
    docpath := path.Join("_doc", r.URL.Path)
    mdpath := docpath + ".md"
    filepath := docpath
    var isDir, isRaw, isMarkdown, found, isEdit, isPost bool
    var mtime time.Time

    query := r.URL.Query()
    isEdit = (query.Get("edit") == "1")
    isPost = (query.Get("post") == "1" && r.Method == "POST")

    if !isEdit && !isPost {
        found = false
        fileInfo, err := os.Lstat(docpath)
        if err == nil {
            if fileInfo.IsDir() {
                found = true
                isDir = true
            } else {
                found = true
                isDir = false
                isRaw = true
            }
        }
        fileInfo, err = os.Lstat(mdpath)
        if err == nil && fileInfo.IsDir() == false && !isRaw {
            found = true
            isDir = false
            isMarkdown = true
            filepath = mdpath
            mtime = fileInfo.ModTime()
        }
    }

    if isPost {
        // save file
        fmt.Println("Save file in ", mdpath)
        err := os.MkdirAll(path.Dir(mdpath), 0755)
        if err != nil {
            fmt.Println(err)
            http.Error(w, "Internal error", 500)
            return
        }

        err = ioutil.WriteFile(mdpath, []byte(r.FormValue("content")), 0644)
        if err != nil {
            fmt.Println(err)
            http.Error(w, "Internal error", 500)
            return
        }
        w.Write([]byte("1"))
    } else if isEdit {
        // serve editor
        fmt.Println("Serve editor for ", filepath)
        http.ServeFile(w, r, "_template/_edit.html")
    } else if !found {
        http.Error(w, "Not found", 404)
        return
    } else if isDir {
        // serve directory
        fmt.Println("Serve dir ", filepath)
    } else if !isMarkdown {
        // serve as static file
        fmt.Println("Serve file ", filepath)
        http.ServeFile(w, r, filepath)
    } else {
        // serve markdown
        fmt.Println("Serve md ", filepath, " ", mtime)
        //http.ServeFile(w, r, *filepath)
        sendMarkdown(w, r.URL.Path, filepath)
    }
}

func main() {
    var err error
    configModTime = time.Now()
    config, err = loadConfig("config.yaml")
    if err != nil {
        fmt.Println("Parse config file error")
        fmt.Println(err)
        return
    }

    go checkConfLoop("config.yaml")

    http.Handle("/_static/", http.StripPrefix("/_static/", http.FileServer(http.Dir("_static"))))
    http.HandleFunc("/", handler)
    http.ListenAndServe(":8010", nil)
}
