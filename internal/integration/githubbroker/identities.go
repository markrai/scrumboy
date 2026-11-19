package githubbroker

import (
    "context"
    "crypto/sha256"
    "crypto/x509"
    "database/sql"
    "errors"
    "net/http"
    "time"
)

const maxCredentialLifetime = 90 * 24 * time.Hour

func (s *BindingsDB) RegisterInstanceCertificate(ctx context.Context,instance string,certificateDER []byte) error {
    if s==nil || s.db==nil || !validInstance(instance) || len(certificateDER)==0 || len(certificateDER)>16<<10{
        return ErrDenied
    }
    certificate,err:=x509.ParseCertificate(certificateDER)
    if err!=nil || certificate.IsCA || certificate.KeyUsage&x509.KeyUsageDigitalSignature==0 {
        return ErrDenied
    }
    allowed:=false
    for _,usage:=range certificate.ExtKeyUsage{
        if usage==x509.ExtKeyUsageClientAuth{allowed=true}
    }
    now:=s.now()
    if !allowed || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) ||
        certificate.NotAfter.After(now.Add(maxCredentialLifetime)){
        return ErrDenied
    }
    hash:=sha256.Sum256(certificateDER)
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return ErrUnavailable}
    defer tx.Rollback()
    var owner string
    var revoked sql.NullInt64
    err=tx.QueryRowContext(ctx,`SELECT instance_id,revoked_at FROM github_instance_certificates
        WHERE certificate_sha256=?`,hash[:]).Scan(&owner,&revoked)
    if err==nil || !errors.Is(err,sql.ErrNoRows){return ErrDenied}
    var active int
    if err:=tx.QueryRowContext(ctx,`SELECT COUNT(*) FROM github_instance_certificates
        WHERE instance_id=? AND revoked_at IS NULL AND expires_at>?`,instance,now.Unix()).Scan(&active);err!=nil{
        return ErrUnavailable
    }
    if active>=2{return ErrDenied}
    _,err=tx.ExecContext(ctx,`INSERT INTO github_instance_certificates(
        certificate_sha256,instance_id,expires_at,registered_at,revoked_at)
        VALUES(?,?,?,?,NULL)`,hash[:],instance,certificate.NotAfter.Unix(),now.Unix())
    if err!=nil{return ErrUnavailable}
    if err:=tx.Commit();err!=nil{return ErrUnavailable}
    return nil
}

func (s *BindingsDB) RevokeInstanceCertificate(ctx context.Context,instance string,certificateDER []byte) error {
    if s==nil || s.db==nil || !validInstance(instance) || len(certificateDER)==0{return ErrDenied}
    hash:=sha256.Sum256(certificateDER)
    result,err:=s.db.ExecContext(ctx,`UPDATE github_instance_certificates
        SET revoked_at=? WHERE instance_id=? AND certificate_sha256=? AND revoked_at IS NULL`,
        s.now().Unix(),instance,hash[:])
    if err!=nil{return ErrUnavailable}
    count,err:=result.RowsAffected()
    if err!=nil || count!=1{return ErrDenied}
    return nil
}

func (s *BindingsDB) DisconnectInstance(ctx context.Context,instance string) error {
    if s==nil || s.db==nil || !validInstance(instance){return ErrDenied}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return ErrUnavailable}
    defer tx.Rollback()
    now:=s.now().Unix()
    if _,err=tx.ExecContext(ctx,`UPDATE github_instance_certificates SET revoked_at=?
        WHERE instance_id=? AND revoked_at IS NULL`,now,instance);err!=nil{return ErrUnavailable}
    if _,err=tx.ExecContext(ctx,`UPDATE github_verified_bindings SET revoked_at=?
        WHERE instance_id=? AND revoked_at IS NULL`,now,instance);err!=nil{return ErrUnavailable}
    if err:=tx.Commit();err!=nil{return ErrUnavailable}
    return nil
}

func (s *BindingsDB) Identify(r *http.Request)(string,error){
    if s==nil || s.db==nil || r==nil || r.TLS==nil || len(r.TLS.PeerCertificates)==0 ||
        len(r.TLS.VerifiedChains)==0{return "",ErrDenied}
    certificate:=r.TLS.PeerCertificates[0]
    if certificate==nil || len(certificate.Raw)==0{return "",ErrDenied}
    digest:=sha256.Sum256(certificate.Raw)
    var instance string
    err:=s.db.QueryRowContext(r.Context(),`SELECT instance_id FROM github_instance_certificates
        WHERE certificate_sha256=? AND revoked_at IS NULL AND expires_at>?`,
        digest[:],s.now().Unix()).Scan(&instance)
    if err!=nil || !validInstance(instance){return "",ErrDenied}
    return instance,nil
}

var _ Authenticator = (*BindingsDB)(nil)
