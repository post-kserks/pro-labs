package executor

// DDL object type constants — canonical values are in types.ObjType*.
// These unexported aliases keep existing call-sites working.
const (
	objTypeView      = "view"
	objTypeTrigger   = "trigger"
	objTypeFunction  = "function"
	objTypeProcedure = "procedure"
)

// systemTableName — name of the virtual table for storing DDL objects.
const systemTableName = "_objects"
