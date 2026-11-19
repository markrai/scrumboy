package githubbroker

import (
    "context"
    "database/sql"
    "errors"
    "os"
    "path/filepath"
    "strings"
    "time"

    "scrumboy/internal/integration"
    "scrumboy/internal/integration/githubconnect"

    _ "modernc.org/sqlite"
)

const bindingLifetime = 24 * time.Hour
const maxBindingRepositories = 2000

type BindingsDB struct {
    db *sql.DB
    now func() time.Time
}

type verifiedRecord struct {
    instance string
    userID int64
    verification githubconnect.Verification
    resources []integration.Resource
}

func OpenBindingsDB(path string) (*BindingsDB, error) {
    if path == "" || !filepath.IsAbs(path) || strings.ContainsAny(path, "?#") {
        return nil, ErrUnavailable
    }
    info, err := os.Lstat(path)
    if err == nil {
        if !info.Mode().IsRegular() || info.Mode().Perm() & 0077 != 0 {
            return nil, ErrUnavailable
        }
    } else if !errors.Is(err, os.ErrNotExist) {
        return nil, ErrUnavailable
    }
    file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
    if err != nil { return nil, ErrUnavailable }
    if err := file.Close(); err != nil { return nil, ErrUnavailable }
    db, err := sql.Open("sqlite", path)
    if err != nil { return nil, ErrUnavailable }
    db.SetMaxOpenConns(1)
    db.SetMaxIdleConns(1)
    store := &BindingsDB{db:db,now:time.Now}
    if err := store.initialize(); err != nil {
        db.Close()
        return nil, ErrUnavailable
    }
    return store,nil
}

func (s *BindingsDB) initialize() error {
    _,err:=s.db.Exec(`PRAGMA foreign_keys=ON;`)
    if err!=nil {return err}
    _,err=s.db.Exec(`PRAGMA busy_timeout=5000;`)
    if err!=nil{return err}
    _,err=s.db.Exec(`CREATE TABLE IF NOT EXISTS github_verified_bindings (
        instance_id TEXT NOT NULL,
        user_id INTEGER NOT NULL CHECK(user_id > 0),
        github_user_id INTEGER NOT NULL CHECK(github_user_id > 0),
        github_login TEXT NOT NULL,
        installation_id INTEGER NOT NULL CHECK(installation_id > 0),
        account_id INTEGER NOT NULL CHECK(account_id > 0),
        account_login TEXT NOT NULL,
        account_type TEXT NOT NULL CHECK(account_type IN ('User','Organization')),
        verified_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        revoked_at INTEGER,
        PRIMARY KEY(instance_id,user_id,installation_id)
    );`)
    if err!=nil{return err}
    _,err=s.db.Exec(`CREATE TABLE IF NOT EXISTS github_verified_repositories (
        instance_id TEXT NOT NULL,
        user_id INTEGER NOT NULL,
        installation_id INTEGER NOT NULL,
        repository_id INTEGER NOT NULL CHECK(repository_id > 0),
        repository TEXT NOT NULL,
        PRIMARY KEY(instance_id,user_id,installation_id,repository_id),
        FOREIGN KEY(instance_id,user_id,installation_id)
          REFERENCES github_verified_bindings(instance_id,user_id,installation_id)
          ON DELETE CASCADE
    );`)
    if err!=nil{return err}
    _,err=s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_github_binding_expires
        ON github_verified_bindings(expires_at,revoked_at);`)
    return err
}

func (s *BindingsDB) Close() error {
    if s==nil || s.db==nil{return nil}
    return s.db.Close()
}

func validVerifiedRecord(r verifiedRecord) bool {
    v:=r.verification
    if !validInstance(r.instance) || r.userID<=0 ||
        v.Subject!=bindingSubject(r.instance,r.userID) ||
        v.GitHubUserID<=0 || v.InstallationID<=0 || v.AccountID<=0 ||
        (v.AccountType!="User" && v.AccountType!="Organization") ||
        len(v.GitHubLogin)==0 || len(v.GitHubLogin)>200 ||
        len(v.AccountLogin)==0 || len(v.AccountLogin)>200 ||
        len(v.AuthorizedRepositoryIDs)>maxBindingRepositories ||
        len(v.AuthorizedRepositoryIDs)!=len(r.resources) {
        return false
    }
    allowed:=make(map[int64]bool,len(v.AuthorizedRepositoryIDs))
    for _,id:=range v.AuthorizedRepositoryIDs{
        if id<=0 || allowed[id]{return false}
        allowed[id]=true
    }
    seen:=make(map[int64]bool,len(r.resources))
    for _,resource:=range r.resources {
        id,err:=parsePositiveID(resource.ID)
        if err!=nil || !allowed[id] || seen[id] ||
            len(resource.Name)>200 || !repositoryNamePattern.MatchString(resource.Name) ||
            strings.Contains(resource.Name,"..") ||
            !strings.EqualFold(resource.URL,"https://github.com/"+resource.Name) {
            return false
        }
        seen[id]=true
    }
    return true
}

func (s *BindingsDB) recordVerified(ctx context.Context,r verifiedRecord) error {
    if s==nil || s.db==nil || !validVerifiedRecord(r){return ErrDenied}
    now:=s.now().UTC()
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return ErrUnavailable}
    defer tx.Rollback()
    var existing int64
    err=tx.QueryRowContext(ctx,`SELECT github_user_id FROM github_verified_bindings
       WHERE instance_id=? AND user_id=? AND installation_id=?`,
        r.instance,r.userID,r.verification.InstallationID).Scan(&existing)
    if err!=nil && !errors.Is(err,sql.ErrNoRows){return ErrUnavailable}
    if err==nil && existing!=r.verification.GitHubUserID{return ErrDenied}
    v:=r.verification
    _,err=tx.ExecContext(ctx,`INSERT INTO github_verified_bindings(
        instance_id,user_id,github_user_id,github_login,installation_id,account_id,
        account_login,account_type,verified_at,expires_at,revoked_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,NULL)
        ON CONFLICT(instance_id,user_id,installation_id) DO UPDATE SET
        github_user_id=excluded.github_user_id,github_login=excluded.github_login,
        account_id=excluded.account_id,account_login=excluded.account_login,
        account_type=excluded.account_type,verified_at=excluded.verified_at,
        expires_at=excluded.expires_at,revoked_at=NULL`,
        r.instance,r.userID,v.GitHubUserID,v.GitHubLogin,v.InstallationID,
        v.AccountID,v.AccountLogin,v.AccountType,now.Unix(),now.Add(bindingLifetime).Unix())
    if err!=nil{return ErrUnavailable}
    _,err=tx.ExecContext(ctx,`DELETE FROM github_verified_repositories
        WHERE instance_id=? AND user_id=? AND installation_id=?`,r.instance,r.userID,v.InstallationID)
    if err!=nil{return ErrUnavailable}
    for _,resource:=range r.resources {
        id,err:=parsePositiveID(resource.ID)
        if err!=nil{return ErrDenied}
        _,err=tx.ExecContext(ctx,`INSERT INTO github_verified_repositories(
            instance_id,user_id,installation_id,repository_id,repository) VALUES(?,?,?,?,?)`,
            r.instance,r.userID,v.InstallationID,id,resource.Name)
        if err!=nil{return ErrUnavailable}
    }
    if err:=tx.Commit();err!=nil{return ErrUnavailable}
    return nil
}

func (s *BindingsDB) LookupUser(ctx context.Context,instance string,userID,installationID,repositoryID int64) (Grant,error) {
    var grant Grant
    if s==nil || s.db==nil || !validInstance(instance) || userID<=0 ||
        installationID<=0 || repositoryID<=0{return grant,ErrDenied}
    var expiresAt int64
    err:=s.db.QueryRowContext(ctx,`SELECT r.repository,b.expires_at FROM github_verified_repositories r
        JOIN github_verified_bindings b ON b.instance_id=r.instance_id AND
           b.user_id=r.user_id AND b.installation_id=r.installation_id
        WHERE r.instance_id=? AND r.user_id=? AND r.installation_id=? AND
          r.repository_id=? AND b.revoked_at IS NULL AND b.expires_at>?`,
        instance,userID,installationID,repositoryID,s.now().UTC().Unix()).Scan(&grant.Repository,&expiresAt)
    if err!=nil{return Grant{},ErrDenied}
    grant.InstanceID=instance
    grant.InstallationID=installationID
    grant.RepositoryID=repositoryID
    grant.ExpiresAt=time.Unix(expiresAt,0).UTC()
    if !validScope(grant,instance,installationID,repositoryID,s.now()){return Grant{},ErrDenied}
    return grant,nil
}

func (s *BindingsDB) revoke(ctx context.Context,query string,args ...any) error {
    if s==nil || s.db==nil{return ErrDenied}
    _,err:=s.db.ExecContext(ctx,query,args...)
    if err!=nil{return ErrUnavailable}
    return nil
}

func (s *BindingsDB) RevokeUser(ctx context.Context,instance string,userID int64) error {
    if !validInstance(instance) || userID<=0{return ErrDenied}
    return s.revoke(ctx,`UPDATE github_verified_bindings SET revoked_at=?
        WHERE instance_id=? AND user_id=? AND revoked_at IS NULL`,
        s.now().UTC().Unix(),instance,userID)
}

func (s *BindingsDB) RevokeInstallation(ctx context.Context,instance string,userID,installationID int64) error {
    if !validInstance(instance) || userID<=0 || installationID<=0{return ErrDenied}
    return s.revoke(ctx,`UPDATE github_verified_bindings SET revoked_at=?
        WHERE instance_id=? AND user_id=? AND installation_id=? AND revoked_at IS NULL`,
        s.now().UTC().Unix(),instance,userID,installationID)
}

func (s *BindingsDB) RevokeInstance(ctx context.Context,instance string) error {
    if !validInstance(instance){return ErrDenied}
    return s.revoke(ctx,`UPDATE github_verified_bindings SET revoked_at=?
        WHERE instance_id=? AND revoked_at IS NULL`,s.now().UTC().Unix(),instance)
}

func (s *BindingsDB) RevokeRepository(ctx context.Context,instance string,userID,installationID,repositoryID int64) error {
    if !validInstance(instance) || userID<=0 || installationID<=0 || repositoryID<=0{return ErrDenied}
    return s.revoke(ctx,`DELETE FROM github_verified_repositories
        WHERE instance_id=? AND user_id=? AND installation_id=? AND repository_id=?`,
        instance,userID,installationID,repositoryID)
}

var _ UserGrants = (*BindingsDB)(nil)
