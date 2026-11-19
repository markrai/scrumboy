package main

import (
    "context"
    "encoding/pem"
    "errors"
    "flag"
    "fmt"
    "os"

    "scrumboy/internal/integration/githubbroker"
)

func execute()error {
    var dbPath,instance,certificatePath,action string
    flag.StringVar(&dbPath,"binding-db","","absolute path to Scrumboy Connect broker SQLite database")
    flag.StringVar(&instance,"instance","","Scrumboy instance identifier")
    flag.StringVar(&certificatePath,"certificate","","public client-auth certificate PEM")
    flag.StringVar(&action,"action","","register, revoke, or disconnect")
    flag.Parse()
    if dbPath=="" || instance=="" || flag.NArg()!=0{return errors.New("missing or unexpected parameters")}
    switch action {
    case "register","revoke":
        if certificatePath=="" {return errors.New("certificate file required")}
    case "disconnect":
        if certificatePath!=""{return errors.New("disconnect does not take a certificate")}
    default:
        return errors.New("unsupported action")
    }
    db,err:=githubbroker.OpenBindingsDB(dbPath)
    if err!=nil{return errors.New("cannot open broker ownership database")}
    defer db.Close()
    ctx:=context.Background()
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
