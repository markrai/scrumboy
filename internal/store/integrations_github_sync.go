package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/integration"
)

func validateGitHubResource(resource integration.Resource) bool {
	id,parseErr:=strconv.ParseInt(resource.ID,10,64)
	if parseErr!=nil || id<=0 || !validIntegrationValue(resource.Name,200) || !validIntegrationURL(resource.URL) {
		return false
	}
	u,err:=url.Parse(resource.URL)
	return err==nil && u.Scheme=="https" && strings.EqualFold(u.Hostname(),"github.com") &&
		u.Port()=="" && u.User==nil && u.RawQuery=="" && u.Fragment==""
}

func (s *Store) SyncGitHubInstallation(ctx context.Context, actorID int64, installation integration.Installation, repositories []integration.Resource) (IntegrationConnection,error) {
	id,parseErr:=strconv.ParseInt(installation.ID,10,64)
	if parseErr!=nil || id<=0 || !validIntegrationValue(installation.Account,200) || len(repositories)>2000 {
		return IntegrationConnection{},fmt.Errorf("%w: invalid installation",ErrValidation)
	}
	ids:=make(map[string]struct{},len(repositories))
	for _,resource:=range repositories {
		key:=resource.ID
		if !validateGitHubResource(resource) {
			return IntegrationConnection{},fmt.Errorf("%w: invalid repository",ErrValidation)
		}
		if _,ok:=ids[key];ok {return IntegrationConnection{},fmt.Errorf("%w: duplicate repository",ErrValidation)}
		ids[key]=struct{}{}
	}
	tx,err:=s.db.BeginTx(ctx,nil)
	if err!=nil{return IntegrationConnection{},err}
	defer tx.Rollback()
	if err:=requireAdminTx(ctx,tx,actorID);err!=nil{return IntegrationConnection{},err}
	now:=time.Now().UTC().UnixMilli()
	installationID:=installation.ID
	_,err=tx.ExecContext(ctx,`INSERT INTO integration_connections(provider,external_account_id,name,status,created_at,updated_at)
		VALUES ('github',?,?,'active',?,?)
		ON CONFLICT(provider,external_account_id) DO UPDATE SET name=excluded.name,status='active',updated_at=excluded.updated_at`,
		installationID,installation.Account,now,now)
	if err!=nil{return IntegrationConnection{},err}
	connection,err:=scanIntegrationConnection(tx.QueryRowContext(ctx,
		"SELECT "+integrationConnectionColumns+" FROM integration_connections WHERE provider='github' AND external_account_id=?",installationID))
	if err!=nil{return IntegrationConnection{},err}
	for _,resource:=range repositories {
		_,err=tx.ExecContext(ctx,`INSERT INTO integration_resources(connection_id,kind,external_id,name,url,created_at,updated_at)
			VALUES (?,'repository',?,?,?,?,?)
			ON CONFLICT(connection_id,kind,external_id) DO UPDATE SET name=excluded.name,url=excluded.url,updated_at=excluded.updated_at`,
			connection.ID,resource.ID,resource.Name,resource.URL,now,now)
		if err!=nil{return IntegrationConnection{},err}
	}
	rows,err:=tx.QueryContext(ctx,
		"SELECT id,external_id FROM integration_resources WHERE connection_id=? AND kind='repository'",connection.ID)
	if err!=nil{return IntegrationConnection{},err}
	var stale []int64
	for rows.Next() {
		var id int64
		var externalID string
		if err:=rows.Scan(&id,&externalID);err!=nil{rows.Close();return IntegrationConnection{},err}
		if _,ok:=ids[externalID];!ok{stale=append(stale,id)}
	}
	err=rows.Err()
	rows.Close()
	if err!=nil{return IntegrationConnection{},err}
	for _,id:=range stale {
		if _,err:=tx.ExecContext(ctx,"DELETE FROM integration_resources WHERE id=? AND connection_id=?",id,connection.ID);err!=nil{return IntegrationConnection{},err}
	}
	return connection,tx.Commit()
}

func (s *Store) DeleteGitHubInstallation(ctx context.Context,actorID,connectionID int64) error {
	if connectionID<=0{return ErrNotFound}
	tx,err:=s.db.BeginTx(ctx,nil)
	if err!=nil{return err}
	defer tx.Rollback()
	if err:=requireAdminTx(ctx,tx,actorID);err!=nil{return err}
	result,err:=tx.ExecContext(ctx,"DELETE FROM integration_connections WHERE id=? AND provider='github'",connectionID)
	if err!=nil{return err}
	n,err:=result.RowsAffected()
	if err!=nil{return err}
	if n==0{return ErrNotFound}
	return tx.Commit()
}

func (s *Store) GitHubInstallationConnection(ctx context.Context,actorID,installationID int64) (IntegrationConnection,error) {
	if installationID<=0{return IntegrationConnection{},ErrNotFound}
	if err:=s.requireAdmin(ctx,actorID);err!=nil{return IntegrationConnection{},err}
	connection,err:=scanIntegrationConnection(s.db.QueryRowContext(ctx,
		"SELECT "+integrationConnectionColumns+" FROM integration_connections WHERE provider='github' AND external_account_id=?",
		strconv.FormatInt(installationID,10)))
	if errors.Is(err,sql.ErrNoRows){return IntegrationConnection{},ErrNotFound}
	return connection,err
}
