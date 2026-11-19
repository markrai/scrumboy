package main

import (
    "context"
    "encoding/pem"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "os"

    "scrumboy/internal/integration/githubbroker"
)

func execute()error {
    var dbPath,instance,certificatePath,action string
    var limit int
    flag.StringVar(&dbPath,"binding-db","","absolute path to Scrumboy Connect broker SQLite database")
    flag.StringVar(&instance,"instance","","Scrumboy instance identifier")
    flag.StringVar(&certificatePath,"certificate","","public client-auth certificate PEM")
    flag.StringVar(&action,"action","","register, revoke, disconnect, or activity")
    flag.IntVar(&limit,"limit",30,"maximum aggregate activity windows to display (1-200)")
    flag.Parse()
    if dbPath=="" || instance=="" || flag.NArg()!=0{return errors.New("missing or unexpected parameters")}
    switch action {
    case "register","revoke":
        if certificatePath=="" {return errors.New("certificate file required")}
    case "disconnect","activity":
        if certificatePath!=""{return errors.New("action does not take a certificate")}
        if action=="activity" && (limit<1 || limit>200){return errors.New("activity limit out of range")}
    default:
        return errors.New("unsupported action")
    }
    db,err:=githubbroker.OpenBindingsDB(dbPath)
    if err!=nil{return errors.New("cannot open broker ownership database")}
    defer db.Close()
    ctx:=context.Background()
    if action=="activity"{
        activity,err:=db.AdmissionActivity(ctx,instance,limit)
        if err!=nil{return errors.New("could not read broker admission activity")}
        if err:=json.NewEncoder(os.Stdout).Encode(activity);err!=nil{
            return errors.New("could not output broker admission activity")
        }
        return nil
    }
    if action=="disconnect"{
        if err:=db.DisconnectInstance(ctx,instance);err!=nil{return errors.New("instance disconnect failed")}
        fmt.Println("Scrumboy Connect instance disconnected")
        return nil
    }
    data,err:=os.ReadFile(certificatePath)
    if err!=nil{return errors.New("cannot read client certificate")}
    block,rest:=pem.Decode(data)
    if block==nil || block.Type!="CERTIFICATE" || len(rest)!=0{
        return errors.New("expected exactly one client certificate PEM")
    }
    if action=="register"{
        err=db.RegisterInstanceCertificate(ctx,instance,block.Bytes)
    }else{
        err=db.RevokeInstanceCertificate(ctx,instance,block.Bytes)
    }
    if err!=nil{return errors.New("certificate operation denied")}
    fmt.Println("Scrumboy Connect instance certificate updated")
    return nil
}

func main(){
    if err:=execute();err!=nil{
        fmt.Fprintln(os.Stderr,"scrumboy-connect-instances:",err)
        os.Exit(1)
    }
}
