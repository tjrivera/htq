// The api package defines the set of functions to be used by HTQ's server and
// worker binaries.

package api

import (
    "os"
    "time"
    "github.com/garyburd/redigo/redis"
    "github.com/gorilla/mux"
    "strconv"
    "fmt"
    "log"
    "net/http"
    "net/url"
)

const (
    REQ_PREFIX string = "htq:requests:"
    DEFAULT_TIMEOUT int = 60
    REQ_SEND_QUEUE string = "htq:send"
    RESP_PREFIX string = "htq:responses"
    REQ_IDS string = "htq:ids"
)

type Task struct {
    Links map[string]string `json:"links"`
    Timeout int `json:"timeout"`
    Method string `json:"method"`
    Status string `json:"status"`
    Uuid string `json:"uuid"`
    Time int64 `json:"timestamp"`
    Headers map[string]string `json:"headers"`
    Data string `json:"data"`
    Url string `json:"url"`
}

func decodeHeaders(h string) (map[string]string, error){
    return make(map[string]string), nil
}

func generateUUID() string {
    f, _ := os.Open("/dev/urandom")
    b := make([]byte, 16)
    f.Read(b)
    f.Close()
    uuid := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
    return uuid
}

func timestamp() int64 {
    return time.Now().UnixNano()
}

func decodeRequest(r *mux.Router, data []string) (Task, error) {
    request := make(map[string]string)
    for i, v := range data {
        if i % 2 == 0 {
            request[v] = data[i+1]
        }
    }
    t_time, err := strconv.ParseInt(request["time"], 10, 64)
    if err != nil {
        log.Println(r)
        return Task{}, err
    }
    t_timeout, err := strconv.Atoi(request["timeout"])
    if err != nil {
        return Task{}, err
    }
    // TODO - handle headers properly
    t_headers, _ := decodeHeaders(request["headers"])
    self_url, _ := r.Get("request").URL("uuid", request["uuid"])
    self_status, _ := r.Get("status").URL("uuid", request["uuid"])
    self_response , _ := r.Get("response").URL("uuid", request["uuid"])

    links := make(map[string]string)
    links["self"] = self_url.Scheme + self_url.Host + self_url.Path
    links["status"] = self_status.Scheme + self_status.Host + self_status.Path
    links["response"] = self_response.Scheme + self_response.Host + self_response.Path

    return Task{
        Status: request["status"],
        Links: links,
        Uuid: request["uuid"],
        Url: request["url"],
        Method: request["method"],
        Time: t_time,
        Headers: t_headers,
        Timeout: t_timeout,
    }, nil
}

func getRedisClient() redis.Conn {
    redisPool := redis.NewPool(func() (redis.Conn,error) {
        c, err := redis.Dial("tcp", ":6379")
        if err != nil{
            return nil, err
        }
        return c, err
    }, 10)

    pool := *redisPool
    return pool.Get()
}

func Size() (int, error) {
    c := getRedisClient()
    size, err := redis.Int(c.Do("LLEN", REQ_SEND_QUEUE))
    if err != nil {
        return -1, err
    } else {
        return size, nil
    }
}

func Request(r *mux.Router, uuid string) (Task, error) {
    c := getRedisClient()
    req, err := redis.Strings(c.Do("HGETALL", REQ_PREFIX + uuid))
    if err != nil || len(req) == 0 {
        return Task{}, fmt.Errorf("Request not found")
    }
    task, err := decodeRequest(r, req)
    if err != nil {
        log.Println("Error decoding request.", err)
    }
    return task, nil
}

func Response(r *mux.Router, uuid string) (Task, error) {
    task, err := Request(r, uuid)
    if err != nil {
        return Task{}, err
    }

    status := task.Status

    for {
        if status != "queued" && status != "pending"{
            break
        }
        time.Sleep(10 * time.Millisecond)
        task, _ := Request(r, uuid)
        status = task.Status
    }
    // Need to implement decodeResponse
    log.Println(status)
    return Task{}, nil
}

func Queued(r *mux.Router) ([]Task, error) {
    c := getRedisClient()
    defer c.Close()
    stop, _ := redis.Int(c.Do("LLEN", "htq:send"))
    queue, _ := redis.Values(c.Do("LRANGE", "htq:send", 0, stop))
    tasks := []Task{}
    for _, task := range queue {
        uuid, _ := redis.String(task, nil)
        data, _ := redis.Strings(c.Do("HGETALL", REQ_PREFIX + uuid))
        task, err := decodeRequest(r, data)
        if err == nil {
            tasks = append(tasks, task)
        } else {
            log.Println("Error:", err)
        }
    }
    return tasks, nil
}

func Cancel(r *mux.Router, uuid string) bool {
    c := getRedisClient()

    key := REQ_PREFIX + uuid

    req, err := redis.Strings(c.Do("HGETALL", key))
    // Request does not exist
    if err != nil {
        log.Println("Unable to retrieve request from redis", err)
        return false
    }
    // Request is malformed.
    task, err := decodeRequest(r, req)
    if err != nil {
        log.Println("Error decoding request", err)
        return false
    }
    // Already canceled or complete without a result
    if task.Status == "canceled" {
        log.Printf("[%v] has already been canceled.", uuid)
        return true
    }
    if task.Status == "success" || task.Status == "timeout" || task.Status == "error" {
        log.Printf("[%v] canceling completed request", uuid)
        c.Send("MULTI")
        c.Send("HSET", key, "status", "canceled")
        c.Send("DEL", RESP_PREFIX + uuid)
        _, err := c.Do("EXEC")
        if err != nil {
            log.Println(err)
            return false
        }
        return true
    }
    // Handle Queued or pended states
    c.Send("WATCH", key)
    c.Send("HSET", key, "status", "canceled")
    c.Flush()
    _, err = c.Receive()
    if err != nil {
        log.Printf("[%v] cancel interrupted, retrying.", uuid)
        log.Println(err)
        return Cancel(r, uuid)
    }

    // If it was only queued, just return since it will be skipped when received.
    if task.Status == "queued" {
        log.Printf("[%v] canceled request", uuid)
        return true
    }

    // Send a delete request that may or may not cancel the previous request
    client := &http.Client{
        Timeout: time.Duration(task.Timeout)*time.Second,
    }

    url, _ := url.Parse(task.Url)
    resp, err := client.Do(&http.Request{
        Method: "DELETE",
        URL: url, //Consider handling this at object struct
        Header: http.Header{}, // TODO
    })
    if err != nil {
        log.Printf("[%v] error sending delete request", uuid)
    } else if 200 <= resp.StatusCode && resp.StatusCode < 300 {
        log.Printf("[%v] successful delete request", uuid)
    } else {
        log.Printf("[%v] error handling delete request", uuid)
    }
    return true
}

func Status(r *mux.Router, uuid string) ([]byte, error) {
    task, err := Request(r, uuid)
    return []byte(`{"status":"`+task.Status+`"}`), err
}

func Purge(uuid string) bool {
    c := getRedisClient()
    r, err := redis.Int(c.Do("DEL", RESP_PREFIX + uuid))
    if err != nil || r == 0{
        if err == nil{
            log.Println("No request found for specified UUID.")
        }
        log.Println("Error:", err)
        return false
    }
    return true
}

// func Receive(uuid string){
//     c := getRedisClient()
//
//
// }
