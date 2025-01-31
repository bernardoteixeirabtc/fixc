/*
FTX exchange FIX plugin
Version : FIX 4.2
Need : fixc 
*/

package main

import (
    "hash"
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "log"
    "flag"
    "encoding/json" 
    "io/ioutil"
    "time"
    "net/http"
    "strconv"
    "strings"
    "./fixc"
)

const SOH = string(1)
var _pFixClient *fixc.FIXClient
var _accessKey string 
var _secretKey string


type RpcRequest struct {
    AccessKey string            `json:"access_key"`
    SecretKey string            `json:"secret_key"`
    Nonce     int64             `json:"nonce"`
    Method    string            `json:"method"`
    Params    map[string]string `json:"params"`
}

type ExecutionReport struct {
    ClOrdID string // Client-selected order ID. 11
    OrderID string // Server-assigned order ID. 37
    Symbol string // Symbol name. 55
    Side string // "1": buy; "2": sell. 54
    OrderQty string // Original order quantity. 38
    Price string // Original order price. 44
    ExecType string // Reason for this message (see below). 150
    /*0	New order
        1	New fill for order
        3	Order done (fully filled)
        4	Order cancelled
        5	Order resized (possible for reduce-only orders)
        A	Response to a successful NewOrderSingle (D) request
        8	Response to a rejected NewOrderSingle (D) request
        6	Response to a successful OrderCancelRequest (F) request
        I	Response to a OrderStatusRequest (H) request
    */
    OrdStatus string // Order status (see below). 39
        /*A	Pending order
        0	New order
        1	Partially filled order
        3	Fully filled order
        4	Cancelled order
        5	Resized order
        6	Pending cancel
    */
    CumQty string // Quantity of order that has already been filled. 14
    LeavesQty string // Quantity of order that is still open. 151
    TransactTime string // Time of the order update. Only present on order updates. 60
    AvgPx string // Average fill price for all fills in order. Only present if this message was the result of a fill. 6
}

var executionReports []ExecutionReport

func HMACEncrypt(pfn func() hash.Hash, data, key string) string {
    h := hmac.New(pfn, []byte(key))
    if _, err := h.Write([]byte(data)); err == nil {
        return hex.EncodeToString(h.Sum(nil))
    }
    return ""
}

func toString(s interface{}) string {
    var ret string
    switch v := s.(type) {
    case string:
        ret = v
    case int64:
        ret = strconv.FormatInt(v, 10)
    case float64:
        ret = strconv.FormatFloat(v, 'f', -1, 64)
    case bool:
        ret = strconv.FormatBool(v)
    default:
        ret = fmt.Sprintf("%v", s)
    }
    return ret
}

func TimeStamp() string {
    t := time.Now().UTC()
    return fmt.Sprintf("%d%02d%02d-%02d:%02d:%02d", t.Year(), int(t.Month()), t.Day(), t.Hour(),t.Minute(), t.Second())   
}

// callback func
func onConnect() {
    // fix logon
    strTime := TimeStamp()
    arr := []string{strTime, "A", "1", _accessKey, "FTX"}
    data := ""
    for _, ele := range arr {
        if ele != "FTX" {
            data += ele + "\x01"
        } else {
            data += ele
        }
    }

    signature := HMACEncrypt(sha256.New, data, _secretKey)   
    // send logon msg
    if err := _pFixClient.Send(fmt.Sprintf("8=|35=A|49=|56=|34=|52=%s|98=0|108=30|96=%s|", strTime, signature)); err != nil {
        fmt.Println("err:", err)
    }
}

func onMessage(fm *fixc.FixMessage) {
    fmt.Println("Receive:", fm.String())

    messageFlag, ok := fm.Find("35");
    messageType, ok := fm.Find("150");

    mClOrdID, ok := fm.Find("11");
    mOrderID, ok := fm.Find("37");
    mSymbol, ok := fm.Find("55");
    mSide, ok := fm.Find("54");
    mOrderQty, ok := fm.Find("38");
    mPrice, ok := fm.Find("44");
    mOrdStatus, ok := fm.Find("39");
    mCumQty, ok := fm.Find("14");
    mLeavesQty, ok := fm.Find("151");
    mTransactTime, ok := fm.Find("60");
    mAvgPx, ok := fm.Find("6");

    if messageFlag == "8" && (messageType == "1" || messageType == "3" || messageType == "4" ){
        
        report := ExecutionReport{
            ClOrdID: mClOrdID,
            OrderID: mOrderID, 
            Symbol: mSymbol,
            Side: mSide,
            OrderQty: mOrderQty, 
            Price: mPrice, 
            ExecType: messageType, 
            OrdStatus: mOrdStatus,
            CumQty: mCumQty, 
            LeavesQty: mLeavesQty, 
            TransactTime: mTransactTime, 
            AvgPx: mAvgPx,
        }

        //executionReports = append(executionReports, report)

        fmt.Println("report", report, ok)
        
    }
}

func onError(err error) {
    fmt.Println(fmt.Sprintf("onError: %v", err))
    return 
}

func tapiCall(method string, params map[string]string) (data interface{}){
    if params == nil {
        params = map[string]string{}
    }

    if method == "/sendOrder" {
        //fmt.Println("ORDER TAPI CALL:", params)
        data = placeCustomTrade(params["market"], params["side"], params["clientId"], params["size"], params["price"], params["postOnly"])
        return
    }
    if method == "/cancelOrderByLabel"{
        //fmt.Println("ORDER TAPI CANCEL:", params)
        data = cancelOrderByLabel(params["label"])
    }
    if method == "/getExecutionFromCache"{
        data = getExecutionFromCache(params["orderId"])
    }
    return
}

func getExecutionFromCache(orderId string) (data interface{}){
    //fmt.Println("SEARCHING ORDER:", orderId)
    for k, v := range executionReports {
        fmt.Println(k, v)
        if (v.OrderID == orderId){
            fmt.Println("FOUND:", v)
            data = v
            return 
        }
    }
    return
}

func placeCustomTrade(contract string, tradeSide string, label string, amount string, price string, postOnly string) (data interface{}){
    var err error
    /* e.g. FTX application interface: new order 
        8=FIX.4.2|9=150|35=D|49=XXXX|56=FTX|34=2|21=1|52=20201111-03:17:14.349|
        11=fmzOrder1112|55=BTC-PERP|40=2|38=0.01|44=8000|54=1|59=1|10=078|
    */ 
    var fm *fixc.FixMessage
    if contract == "" {
        panic("symbol is empty!")
    }
    msgType := "D"
    if tradeSide == "buy" {
        tradeSide = "1"
    } else {
        tradeSide = "2"
    }

    msg := new(fixc.MsgBase)
    msg.AddField(35, msgType) // Standard Never Changes
    msg.AddField(21, "1") // Standard Never Changes
    msg.AddField(11, label) // Clientorder Id
    msg.AddField(55, strings.ToUpper(contract)) // Contract
    msg.AddField(40, "2") // Order type always limit 
    msg.AddField(38, toString(amount)) // Amount 
    msg.AddField(44, toString(price)) // Price
    msg.AddField(54, tradeSide) // Side
    msg.AddField(59, "1") // GTC  

    if postOnly == "true" {
        msg.AddField(18, "6") // Post Only
    }

    _pFixClient.Send(fmt.Sprintf("|8=|49=|56=|34=|52=|%s", msg.Pack()))   // new order
    fm, err = _pFixClient.Expect("11=" + label)         // waiting msg "35=8", "150=A", 
    if err != nil {
        panic(fmt.Sprintf("%v", err))
    }

    //fmt.Println("Order response:", fm.String())

    // analysis
    if orderId, ok := fm.Find("37"); ok {
        //fmt.Println("Order Successfully Created:", orderId, fm.String())
        data = map[string]string{"id": orderId}
        return
    } else if messageError, ok := fm.Find("58"); ok {
        //fmt.Println("Order Rejected:", messageError, fm.String())
        data = map[string]string{"error": messageError}
        return
    } else {
        //fmt.Println("Unhandled Rejection:", fm.String())
        panic(fmt.Sprintf("%s", fm.String()))
    }

}

func placeStandardTrade(s string, request RpcRequest) (data interface{}) {
    var err error
    /* e.g. FTX application interface: new order 
        8=FIX.4.2|9=150|35=D|49=XXXX|56=FTX|34=2|21=1|52=20201111-03:17:14.349|
        11=fmzOrder1112|55=BTC-PERP|40=2|38=0.01|44=8000|54=1|59=1|10=078|
    */ 
    var fm *fixc.FixMessage
    if s == "" {
        panic("symbol is empty!")
    }
    msgType := "D"   
    tradeSids := request.Params["type"]
    if tradeSids == "buy" {
        tradeSids = "1"
    } else {
        tradeSids = "2"
    }
    ts := time.Now().UnixNano() / 1e6                                          
    msg := new(fixc.MsgBase)
    msg.AddField(35, msgType)
    msg.AddField(21, "1")
    msg.AddField(11, fmt.Sprintf("fmz%d", ts))
    msg.AddField(55, strings.ToUpper(s))
    msg.AddField(40, "2")
    msg.AddField(38, toString(request.Params["amount"]))
    msg.AddField(44, toString(request.Params["price"]))
    msg.AddField(54, tradeSids)
    msg.AddField(59, "1")
    _pFixClient.Send(fmt.Sprintf("8=|49=|56=|34=|52=|%s", msg.Pack()))   // new order
    fm, err = _pFixClient.Expect("35=8", "150=A")                        // waiting msg
    if err != nil {
        panic(fmt.Sprintf("%v", err))
    }
    // analysis
    if orderId, ok := fm.Find("37"); ok {
        data = map[string]string{"id": orderId}
        return
    } else {
        panic(fmt.Sprintf("%s", fm.String()))
    }
}

func cancelOrderByLabel(label string) (data interface{}){
    //fmt.Println("CANCEL ORDER:", label)
    var err error

    msg := new(fixc.MsgBase)
    msg.AddField(35, "F")
    msg.AddField(41, label)
    _pFixClient.Send(fmt.Sprintf("8=|49=|56=|34=|52=|%s", msg.Pack()))   // cancel order 
    _, err = _pFixClient.Expect("35=8", "150=6")
    if err != nil {
        panic(fmt.Sprintf("%v", err))
    }
    return true
}

func cancelOrder(orderId string) (data interface{}){
    fmt.Println("CANCEL ORDER:", orderId)
    var err error

    msg := new(fixc.MsgBase)
    msg.AddField(35, "F")
    msg.AddField(37, orderId)
    _pFixClient.Send(fmt.Sprintf("8=|49=|56=|34=|52=|%s", msg.Pack()))   // cancel order 
    _, err = _pFixClient.Expect("37=" + orderId, "35=8", "150=6")
    if err != nil {
        panic(fmt.Sprintf("%v", err))
    }
    return true
}

func OnPost(w http.ResponseWriter, r *http.Request) {
    var ret interface{}
    defer func() {
        if e := recover(); e != nil {
            if ee, ok := e.(error); ok {
                e = ee.Error()
            }
            ret = map[string]string{"error": fmt.Sprintf("%v", e)}
        }

        b, _ := json.Marshal(ret)
        w.Write(b)
    }()

    b, err := ioutil.ReadAll(r.Body)
    if err != nil {
        panic(err)
    }
    var request RpcRequest
    err = json.Unmarshal(b, &request)
    if err != nil {
        panic(err)
    }

    if len(request.AccessKey) > 0 {
        _accessKey = request.AccessKey
    }
    if len(request.SecretKey) > 0 {
        _secretKey = request.SecretKey
    }
    
    var symbol string 
    if _, ok := request.Params["symbol"]; ok {
        symbol = request.Params["symbol"]
    }

    // first create FixClient
    if _pFixClient == nil {
        _pFixClient = fixc.NewFixClient(time.Second * 30, time.Second * 30, "4.2", "fix.ftx.com:4363", _accessKey, "FTX")
        // Start FixClient 
        _pFixClient.Start(onConnect, onMessage, onError)
        time.Sleep(time.Second * 2)
    }

    // processing for FMZ api , exchange.Buy / exchange.Sell , exchange.CancelOrder and so on 
    var data interface{}
    switch request.Method {
    case "trade":
        data = placeStandardTrade(symbol, request)
    case "cancel":        
        orderId := request.Params["id"]
        data = cancelOrder(orderId)
        data = true
    case "order":
        //orderId := request.Params["id"]
        data = true

    default:
        if strings.HasPrefix(request.Method, "__api_") {
            data = tapiCall(request.Method[6:], request.Params)
        } else {
            //panic(errors.New(request.Method + " not support"))
        }
    }

    // response to the robot request 
    ret = map[string]interface{}{
        "data": data,
    }
}

func main() {
    var addr = flag.String("b", "127.0.0.1:8888", "bind addr")
    flag.Parse()
    if *addr == "" {
        flag.Usage()
        return 
    }
    basePath := "/FTX"
    log.Println("Running ", fmt.Sprintf("http://%s%s", *addr, basePath), "...")
    http.HandleFunc(basePath, OnPost)
    http.ListenAndServe(*addr, nil)
}
