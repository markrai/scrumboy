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
		_,err=tx.ExecContext(ctx,`INSERT INTO integration_resources(connection_id,kind,external_id,name,url,created_at,updated_at,availability)
			VALUES (?,'repository',?,?,?,?,?,'available')
			ON CONFLICT(connection_id,kind,external_id) DO UPDATE SET name=excluded.name,url=excluded.url,availability='available',updated_at=excluded.updated_at`,
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
		if _,err:=tx.ExecContext(ctx,"UPDATE integration_resources SET availability='unavailable',updated_at=? WHERE id=? AND connection_id=? AND availability='available'",now,id,connection.ID);err!=nil{return IntegrationConnection{},err}
	}
	return connection,tx.Commit()
}

type GitHubDisconnectImpact struct {
	Repositories int64 `json:"repositories"`
	ProjectAssociations int64 `json:"projectAssociations"`
	CardReferences int64 `json:"cardReferences"`
}

func githubDisconnectImpactTx(ctx context.Context, tx *sql.Tx, connectionID int64) (GitHubDisconnectImpact,error) {
	var out GitHubDisconnectImpact
	err:=tx.QueryRowContext(ctx,`SELECT
		(SELECT COUNT(*) FROM integration_resources WHERE connection_id=c.id),
		(SELECT COUNT(*) FROM integration_project_resources pr JOIN integration_resources r ON r.id=pr.resource_id WHERE r.connection_id=c.id),
		(SELECT COUNT(*) FROM integration_todo_resources tr JOIN integration_resources r ON r.id=tr.resource_id WHERE r.connection_id=c.id)
		FROM integration_connections c WHERE c.id=? AND c.provider='github'`,connectionID).
		Scan(&out.Repositories,&out.ProjectAssociations,&out.CardReferences)
	if errors.Is(err,sql.ErrNoRows){return out,ErrNotFound}
	return out,err
}

func (s *Store) GetGitHubDisconnectImpact(ctx context.Context,actorID,connectionID int64) (GitHubDisconnectImpact,error) {
	if connectionID<=0{return GitHubDisconnectImpact{},ErrNotFound}
	tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
	if err!=nil{return GitHubDisconnectImpact{},err}
	defer tx.Rollback()
	if err:=requireAdminTx(ctx,tx,actorID);err!=nil{return GitHubDisconnectImpact{},err}
	impact,err:=githubDisconnectImpactTx(ctx,tx,connectionID)
	if err!=nil{return GitHubDisconnectImpact{},err}
	return impact,tx.Commit()
}

func (s *Store) DeleteGitHubInstallation(ctx context.Context,actorID,connectionID int64,expected GitHubDisconnectImpact) error {
	if connectionID<=0{return ErrNotFound}
	tx,err:=s.db.BeginTx(ctx,nil)
	if err!=nil{return err}
	defer tx.Rollback()
	if err:=requireAdminTx(ctx,tx,actorID);err!=nil{return err}
	impact,err:=githubDisconnectImpactTx(ctx,tx,connectionID)
	if err!=nil{return err}
	if impact!=expected{return ErrConflict}
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
