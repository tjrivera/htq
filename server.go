package main

import (
    // "flag"
    "encoding/json"
    "net/http"
    "log"
    "os"
    "github.com/gorilla/mux"
    "github.com/gorilla/handlers"
    "./api"
    "fmt"
)

func router() *mux.Router {
    router := mux.NewRouter()
    router.Host("http://localhost:9000")
    router.HandleFunc("/", IndexHandler).Methods("GET", "POST")
    router.HandleFunc("/{uuid}/", RequestHandler).Name("request").Methods("GET", "DELETE")
    router.HandleFunc("/{uuid}/status/", RequestStatusHandler).Name("status").Methods("GET", "POST")
    router.HandleFunc("/{uuid}/response/", ResponseHandler).Name("response").Methods("GET", "POST")
    return router
}

// TODO: Investigate this piece further to better understand interfaces
func routerHandler(router *mux.Router) http.HandlerFunc {
    return func(res http.ResponseWriter, req *http.Request){
        router.ServeHTTP(res, req)
    }
}

func IndexHandler(res http.ResponseWriter, req *http.Request) {
    if req.Method == "GET"{
        tasks, _ := api.Queued(router())
        data, _ := json.Marshal(tasks)
        res.Header().Set("Content-Type", "application/json; charset=utf-8")
        res.Write(data)
    }
}

func RequestHandler(res http.ResponseWriter, req *http.Request) {
    vars := mux.Vars(req)
    uuid := vars["uuid"]
    task, err := api.Request(router(), uuid)
    if err != nil {
        http.Error(res, err.Error(), http.StatusNotFound)
        return
    }
    res.Header().Set("Content-Type", "application/json")
    if req.Method == "GET"{
        data, _ := json.Marshal(task)
        res.Write(data)
        return
    } else if req.Method == "DELETE"{
        r := api.Cancel(router(), uuid)
        if r {
            res.Write([]byte("WIP"))
        }
        return
    }
}

func RequestStatusHandler(res http.ResponseWriter, req *http.Request) {
    vars := mux.Vars(req)
    uuid := vars["uuid"]
    res.Header().Set("Content-Type", "application/json")
    status, err := api.Status(router(), uuid)
    if err != nil {
        http.Error(res, err.Error(), http.StatusNotFound)
        return
    }
    res.Write(status)
    return
}
func ResponseHandler(res http.ResponseWriter, req *http.Request) {
    vars := mux.Vars(req)
    uuid := vars["uuid"]
    res.Header().Set("Content-Type", "application/json")
    response, err := api.Response(router(), uuid)
    if err != nil {
        fmt.Println("Error:", err)
    }
    data, _ := json.Marshal(response)
    res.Write(data)
    return
}

func main(){
    handler := routerHandler(router())
    fmt.Println("htq server started on port "+ os.Getenv("PORT")+ "...")
    err := http.ListenAndServe(":"+os.Getenv("PORT"), handlers.LoggingHandler(os.Stdout, handler))
    if err != nil {
        log.Fatal("ListenAndServe: ", err)
    }
}
