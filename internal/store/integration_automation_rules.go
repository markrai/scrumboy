package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxIntegrationAutomationRules = 50

type IntegrationAutomationRule struct {
	ID int64 `json:"id"`
	ProjectID int64 `json:"projectId"`
	CreatedByUserID *int64 `json:"createdByUserId"`
	ResourceID int64 `json:"resourceId"`
	Name string `json:"name"`
	EventType string `json:"eventType"`
	ActionType string `json:"actionType"`
	Enabled bool `json:"enabled"`
    ExecutionApprovedAt *time.Time `json:"executionApprovedAt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CreateIntegrationAutomationRuleInput struct {
	ResourceID int64 `json:"resourceId"`
	Name string `json:"name"`
	EventType string `json:"eventType"`
	ActionType string `json:"actionType"`
}

type UpdateIntegrationAutomationRuleInput struct {
	ResourceID *int64 `json:"resourceId,omitempty"`
	Name *string `json:"name,omitempty"`
	EventType *string `json:"eventType,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
}

type IntegrationAutomationPreviewInput struct {
	ResourceID int64 `json:"resourceId"`
	EventType string `json:"eventType"`
}

type IntegrationAutomationPreview struct {
	RuleID int64 `json:"ruleId"`
	SampleOnly bool `json:"sampleOnly"`
	Matched bool `json:"matched"`
	Enabled bool `json:"enabled"`
	ActionType string `json:"actionType"`
	ExecutionAvailable bool `json:"executionAvailable"`
	WouldExecute bool `json:"wouldExecute"`
	Reason string `json:"reason"`
}

func validIntegrationAutomationEvent(event string) bool {
	switch event {
	case "issue.opened","issue.closed","pull_request.opened","pull_request.closed","pull_request.merged":
		return true
	default:
		return false
	}
}

func validIntegrationAutomationName(name string) bool {
	if !validIntegrationValue(name,100) {return false}
	for _,r:=range name {
		if r=='\u2028'||r=='\u2029' {return false}
	}
	return true
}

func validIntegrationAutomationRule(in CreateIntegrationAutomationRuleInput) bool {
	return in.ResourceID>0 && validIntegrationAutomationName(in.Name) &&
		validIntegrationAutomationEvent(in.EventType) && in.ActionType=="notify"
}

func scanIntegrationAutomationRule(row interface{Scan(...any)error}) (IntegrationAutomationRule,error) {
	var rule IntegrationAutomationRule
	var creator sql.NullInt64
	var created,updated int64
    var approved sql.NullInt64
    err:=row.Scan(&rule.ID,&rule.ProjectID,&creator,&rule.ResourceID,
        &rule.Name,&rule.EventType,&rule.ActionType,&rule.Enabled,&created,&updated,&approved)
	if errors.Is(err,sql.ErrNoRows){return rule,ErrNotFound}
	if err!=nil{return rule,err}
	if creator.Valid {id:=creator.Int64;rule.CreatedByUserID=&id}
	rule.CreatedAt=time.UnixMilli(created).UTC()
	rule.UpdatedAt=time.UnixMilli(updated).UTC()
    if approved.Valid{v:=time.UnixMilli(approved.Int64).UTC();rule.ExecutionApprovedAt=&v}
	return rule,nil
}

const integrationAutomationColumns="id,project_id,created_by_user_id,resource_id,name,event_type,action_type,enabled,created_at,updated_at,(SELECT approved_at FROM integration_automation_execution_approvals ap WHERE ap.rule_id=integration_automation_rules.id AND ap.rule_updated_at=integration_automation_rules.updated_at)"

func integrationAutomationResourceTx(ctx context.Context,tx *sql.Tx,projectID,resourceID int64)error{
	if resourceID<=0{return ErrValidation}
	var found int64
	err:=tx.QueryRowContext(ctx,`SELECT r.id FROM integration_project_resources pr
		JOIN integration_resources r ON r.id=pr.resource_id
		JOIN integration_connections c ON c.id=r.connection_id
		WHERE pr.project_id=? AND r.id=? AND r.kind='repository'
		  AND r.availability='available' AND c.status='active'
		  AND c.provider='github'`,projectID,resourceID).Scan(&found)
	if errors.Is(err,sql.ErrNoRows){return ErrNotFound}
	return err
}

func automationRuleByIDTx(ctx context.Context,tx *sql.Tx,projectID,ruleID int64)(IntegrationAutomationRule,error){
	return scanIntegrationAutomationRule(tx.QueryRowContext(ctx,
		"SELECT "+integrationAutomationColumns+" FROM integration_automation_rules WHERE project_id=? AND id=?",
		projectID,ruleID))
}

func (s *Store) CreateIntegrationAutomationRule(ctx context.Context,actorID,projectID int64,in CreateIntegrationAutomationRuleInput)(IntegrationAutomationRule,error){
	if !validIntegrationAutomationRule(in){return IntegrationAutomationRule{},fmt.Errorf("%w: invalid automation rule",ErrValidation)}
	tx,err:=s.db.BeginTx(ctx,nil)
	if err!=nil{return IntegrationAutomationRule{},err}
	defer tx.Rollback()
	if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,true);err!=nil{return IntegrationAutomationRule{},err}
	if err:=integrationAutomationResourceTx(ctx,tx,projectID,in.ResourceID);err!=nil{return IntegrationAutomationRule{},err}
	var count int64
	if err:=tx.QueryRowContext(ctx,"SELECT COUNT(*) FROM integration_automation_rules WHERE project_id=?",projectID).Scan(&count);err!=nil{
		return IntegrationAutomationRule{},err
	}
	if count>=maxIntegrationAutomationRules {return IntegrationAutomationRule{},ErrValidation}
	now:=time.Now().UTC().UnixMilli()
	result,err:=tx.ExecContext(ctx,`INSERT INTO integration_automation_rules(
		project_id,created_by_user_id,resource_id,name,event_type,action_type,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?, ?,0,?,?)`,projectID,actorID,in.ResourceID,in.Name,in.EventType,in.ActionType,now,now)
	if err!=nil{return IntegrationAutomationRule{},err}
	id,err:=result.LastInsertId()
	if err!=nil{return IntegrationAutomationRule{},err}
	rule,err:=automationRuleByIDTx(ctx,tx,projectID,id)
	if err!=nil{return IntegrationAutomationRule{},err}
	if err:=tx.Commit();err!=nil{return IntegrationAutomationRule{},err}
	return rule,nil
}

func (s *Store) ListIntegrationAutomationRules(ctx context.Context,actorID,projectID int64)([]IntegrationAutomationRule,error){
	tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
	if err!=nil{return nil,err}
	defer tx.Rollback()
	if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,false);err!=nil{return nil,err}
	rows,err:=tx.QueryContext(ctx,"SELECT "+integrationAutomationColumns+
		" FROM integration_automation_rules WHERE project_id=? ORDER BY id",projectID)
	if err!=nil{return nil,err}
	result:=make([]IntegrationAutomationRule,0)
	for rows.Next(){
		rule,err:=scanIntegrationAutomationRule(rows)
		if err!=nil{rows.Close();return nil,err}
		result=append(result,rule)
	}
	err=rows.Err()
	rows.Close()
	if err!=nil{return nil,err}
	if err:=tx.Commit();err!=nil{return nil,err}
	return result,nil
}

func (s *Store) UpdateIntegrationAutomationRule(ctx context.Context,actorID,projectID,ruleID int64,in UpdateIntegrationAutomationRuleInput)(IntegrationAutomationRule,error){
	if ruleID<=0 || (in.ResourceID==nil && in.Name==nil && in.EventType==nil && in.Enabled==nil){
		return IntegrationAutomationRule{},ErrValidation
	}
	tx,err:=s.db.BeginTx(ctx,nil)
	if err!=nil{return IntegrationAutomationRule{},err}
	defer tx.Rollback()
	if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,true);err!=nil{return IntegrationAutomationRule{},err}
	current,err:=automationRuleByIDTx(ctx,tx,projectID,ruleID)
	if err!=nil{return IntegrationAutomationRule{},err}
	if in.ResourceID!=nil{current.ResourceID=*in.ResourceID}
	if in.Name!=nil{current.Name=*in.Name}
	if in.EventType!=nil{current.EventType=*in.EventType}
	if in.Enabled!=nil{current.Enabled=*in.Enabled}
	if !validIntegrationAutomationRule(CreateIntegrationAutomationRuleInput{
		ResourceID:current.ResourceID,Name:current.Name,EventType:current.EventType,ActionType:current.ActionType,
	}){return IntegrationAutomationRule{},ErrValidation}
	if in.ResourceID!=nil || (in.Enabled!=nil && *in.Enabled) {
		if err:=integrationAutomationResourceTx(ctx,tx,projectID,current.ResourceID);err!=nil{return IntegrationAutomationRule{},err}
	}
	if _,err:=tx.ExecContext(ctx,"DELETE FROM integration_automation_execution_approvals WHERE rule_id=?",ruleID);err!=nil{return IntegrationAutomationRule{},err}
    _,err=tx.ExecContext(ctx,`UPDATE integration_automation_rules
		SET resource_id=?,name=?,event_type=?,enabled=?,updated_at=?
		WHERE project_id=? AND id=?`,current.ResourceID,current.Name,current.EventType,current.Enabled,
		time.Now().UTC().UnixMilli(),projectID,ruleID)
	if err!=nil{return IntegrationAutomationRule{},err}
	rule,err:=automationRuleByIDTx(ctx,tx,projectID,ruleID)
	if err!=nil{return IntegrationAutomationRule{},err}
	if err:=tx.Commit();err!=nil{return IntegrationAutomationRule{},err}
	return rule,nil
}

func (s *Store) DeleteIntegrationAutomationRule(ctx context.Context,actorID,projectID,ruleID int64)error{
	if ruleID<=0{return ErrValidation}
	tx,err:=s.db.BeginTx(ctx,nil)
	if err!=nil{return err}
	defer tx.Rollback()
	if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,true);err!=nil{return err}
	result,err:=tx.ExecContext(ctx,
		"DELETE FROM integration_automation_rules WHERE project_id=? AND id=?",projectID,ruleID)
	if err!=nil{return err}
	n,err:=result.RowsAffected()
	if err!=nil{return err}
	if n!=1{return ErrNotFound}
	return tx.Commit()
}

func (s *Store) PreviewIntegrationAutomationRule(ctx context.Context,actorID,projectID,ruleID int64,in IntegrationAutomationPreviewInput)(IntegrationAutomationPreview,error){
	if ruleID<=0 || in.ResourceID<=0 || !validIntegrationAutomationEvent(in.EventType){
		return IntegrationAutomationPreview{},ErrValidation
	}
	tx,err:=s.db.BeginTx(ctx,&sql.TxOptions{ReadOnly:true})
	if err!=nil{return IntegrationAutomationPreview{},err}
	defer tx.Rollback()
	if err:=s.checkIntegrationProjectTx(ctx,tx,actorID,projectID,false);err!=nil{
		return IntegrationAutomationPreview{},err
	}
	rule,err:=automationRuleByIDTx(ctx,tx,projectID,ruleID)
	if err!=nil{return IntegrationAutomationPreview{},err}
	preview:=IntegrationAutomationPreview{
		RuleID:rule.ID,SampleOnly:true,Enabled:rule.Enabled,ActionType:rule.ActionType,
		ExecutionAvailable:false,WouldExecute:false,Reason:"sample_does_not_match",
	}
	preview.Matched=rule.ResourceID==in.ResourceID && strings.EqualFold(rule.EventType,in.EventType)
	if preview.Matched {
		if err:=integrationAutomationResourceTx(ctx,tx,projectID,rule.ResourceID);err!=nil{
			if !errors.Is(err,ErrNotFound){return IntegrationAutomationPreview{},err}
			preview.Matched=false
			preview.Reason="resource_unavailable"
		} else if !rule.Enabled {
			preview.Reason="rule_disabled"
		} else {
			if rule.ExecutionApprovedAt==nil{preview.Reason="execution_not_approved"}else{preview.Reason="approved_sample_only"}
		}
	}
	if err:=tx.Commit();err!=nil{return IntegrationAutomationPreview{},err}
	return preview,nil
}
