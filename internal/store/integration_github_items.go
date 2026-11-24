package store

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "strings"
    "time"

    "scrumboy/internal/integration"
)

type GitHubProjectRepository struct {
    ResourceID int64
    ConnectionID int64
    InstallationID string
    ExternalID string
    Name string
}

func (s *Store) GetGitHubProjectRepository(ctx context.Context, actorID, projectID, resourceID int64, manage bool) (GitHubProjectRepository,error) {
    var repo GitHubProjectRepository
    if resourceID<=0{return repo,ErrNotFound}
    tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
    if err!=nil{return repo,err}
    defer tx.Rollback()
    if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,false);err!=nil{return repo,err}
    if manage {
        role,err:=s.getProjectRoleTx(ctx,tx,projectID,actorID)
        if err!=nil{return repo,err}
        if !role.HasMinimumRole(RoleContributor){return repo,ErrForbidden}
    }
    err=tx.QueryRowContext(ctx,`SELECT r.id,r.connection_id,c.external_account_id,r.external_id,r.name
        FROM integration_project_resources pr
        JOIN integration_resources r ON r.id=pr.resource_id
        JOIN integration_connections c ON c.id=r.connection_id
        WHERE pr.project_id=? AND r.id=? AND r.kind='repository' AND r.availability='available'
          AND c.provider='github' AND c.status='active'`,projectID,resourceID).
        Scan(&repo.ResourceID,&repo.ConnectionID,&repo.InstallationID,&repo.ExternalID,&repo.Name)
    if errors.Is(err,sql.ErrNoRows){return repo,ErrNotFound}
    if err!=nil{return repo,err}
    return repo,tx.Commit()
}

func (s *Store) ListTodoGitHubItems(ctx context.Context, actorID, projectID, localID int64) ([]integration.GitHubItem,error) {
    tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
    if err!=nil{return nil,err}
    defer tx.Rollback()
    todoID,err:=s.integrationTodoTx(ctx,tx,actorID,projectID,localID,false)
    if err!=nil{return nil,err}
    rows,err:=tx.QueryContext(ctx,`SELECT i.resource_id,i.kind,i.number,i.title,i.state,i.draft,i.merged,i.url,r.name,r.availability
        FROM integration_todo_github_items i JOIN integration_resources r ON r.id=i.resource_id
        WHERE i.project_id=? AND i.todo_id=? ORDER BY i.resource_id,i.kind,i.number`,projectID,todoID)
    if err!=nil{return nil,err}
    result:=make([]integration.GitHubItem,0)
    for rows.Next() {
        var item integration.GitHubItem
        var draft,merged int
        if err:=rows.Scan(&item.ResourceID,&item.Kind,&item.Number,&item.Title,&item.State,&draft,&merged,&item.URL,&item.Repository,&item.Availability);err!=nil{
            rows.Close();return nil,err
        }
        item.Draft=draft==1
        item.Merged=merged==1
        result=append(result,item)
    }
    err=rows.Err()
    rows.Close()
    if err!=nil{return nil,err}
    return result,tx.Commit()
}

func validStoredGitHubItem(item integration.GitHubItem) bool {
    if item.ResourceID<=0 || item.Number<=0 || item.Number>2147483647 ||
       (item.Kind!="issue" && item.Kind!="pr") || len(item.Title)==0 || len(item.Title)>500 ||
       (item.State!="open" && item.State!="closed") || len(item.URL)>2048 {return false}
    if strings.ContainsRune(item.Title,0){return false}
    return true
}

func (s *Store) LinkTodoGitHubItem(ctx context.Context, actorID, projectID, localID int64, item integration.GitHubItem) error {
    if !validStoredGitHubItem(item){return fmt.Errorf("%w: invalid GitHub item",ErrValidation)}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return err}
    defer tx.Rollback()
    todoID,err:=s.integrationTodoTx(ctx,tx,actorID,projectID,localID,true)
    if err!=nil{return err}
    var name string
    err=tx.QueryRowContext(ctx,`SELECT r.name FROM integration_project_resources pr
        JOIN integration_resources r ON r.id=pr.resource_id
        JOIN integration_connections c ON c.id=r.connection_id
        WHERE pr.project_id=? AND r.id=? AND r.kind='repository' AND r.availability='available'
          AND c.provider='github' AND c.status='active'`,projectID,item.ResourceID).Scan(&name)
    if errors.Is(err,sql.ErrNoRows){return ErrNotFound}
    if err!=nil{return err}
    segment:="issues"
    if item.Kind=="pr"{segment="pull"}
    canonical:=fmt.Sprintf("https://github.com/%s/%s/%d",name,segment,item.Number)
    if !strings.EqualFold(canonical,item.URL){return ErrValidation}
    var count int
    err=tx.QueryRowContext(ctx,"SELECT COUNT(*) FROM integration_todo_github_items WHERE todo_id=?",todoID).Scan(&count)
    if err!=nil{return err}
    var found int
    err=tx.QueryRowContext(ctx,`SELECT COUNT(*) FROM integration_todo_github_items
        WHERE todo_id=? AND resource_id=? AND kind=? AND number=?`,todoID,item.ResourceID,item.Kind,item.Number).Scan(&found)
    if err!=nil{return err}
    if found==0 && count>=50{return ErrValidation}
    now:=time.Now().UTC().UnixMilli()
    _,err=tx.ExecContext(ctx,`INSERT INTO integration_todo_github_items
        (project_id,todo_id,resource_id,kind,number,title,state,draft,merged,url,updated_at)
        VALUES(?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(todo_id,resource_id,kind,number)
        DO UPDATE SET title=excluded.title,state=excluded.state,draft=excluded.draft,
            merged=excluded.merged,url=excluded.url,updated_at=excluded.updated_at`,
        projectID,todoID,item.ResourceID,item.Kind,item.Number,item.Title,item.State,item.Draft,item.Merged,item.URL,now)
    if err!=nil{return err}
    return tx.Commit()
}

func (s *Store) UnlinkTodoGitHubItem(ctx context.Context, actorID, projectID, localID, resourceID int64,kind string,number int64) error {
    if resourceID<=0 || number<=0 || (kind!="issue" && kind!="pr"){return ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return err}
    defer tx.Rollback()
    todoID,err:=s.integrationTodoTx(ctx,tx,actorID,projectID,localID,true)
    if err!=nil{return err}
    result,err:=tx.ExecContext(ctx,`DELETE FROM integration_todo_github_items
        WHERE project_id=? AND todo_id=? AND resource_id=? AND kind=? AND number=?`,
        projectID,todoID,resourceID,kind,number)
    if err!=nil{return err}
    n,err:=result.RowsAffected()
    if err!=nil{return err}
    if n==0{return ErrNotFound}
    return tx.Commit()
}

func (s *Store) RefreshTodoGitHubItem(ctx context.Context,actorID,projectID,localID int64,item integration.GitHubItem) error {
    if !validStoredGitHubItem(item){return ErrValidation}
    tx,err:=s.db.BeginTx(ctx,nil)
    if err!=nil{return err}
    defer tx.Rollback()
    todoID,err:=s.integrationTodoTx(ctx,tx,actorID,projectID,localID,true)
    if err!=nil{return err}
    result,err:=tx.ExecContext(ctx,`UPDATE integration_todo_github_items
        SET title=?,state=?,draft=?,merged=?,url=?,updated_at=?
        WHERE project_id=? AND todo_id=? AND resource_id=? AND kind=? AND number=?`,
        item.Title,item.State,item.Draft,item.Merged,item.URL,time.Now().UTC().UnixMilli(),
        projectID,todoID,item.ResourceID,item.Kind,item.Number)
    if err!=nil{return err}
    n,err:=result.RowsAffected()
    if err!=nil{return err}
    if n==0{return ErrNotFound}
    return tx.Commit()
}
