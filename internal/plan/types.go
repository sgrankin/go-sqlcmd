package plan

// Result holds the analysis of one or more execution plan documents.
type Result struct {
	Statements []StatementResult
}

// StatementResult holds the analysis of a single statement.
type StatementResult struct {
	Attrs         map[string]string // StmtSimple attributes
	SetOptions    map[string]string // StatementSetOptions attributes
	PlanAttrs     map[string]string // QueryPlan attributes
	MemoryGrant   map[string]string // MemoryGrantInfo attributes
	HardwareProps map[string]string // OptimizerHardwareDependentProperties
	Warnings      []Warning         // Plan-level warnings
	TimeStats     map[string]string // QueryTimeStats attributes
	StatsRefCount int               // Number of OptimizerStatsUsage/StatisticsInfo elements
	HasActualInfo bool              // True if RunTimeInformation is present
	Root          *Operator         // Root of the operator tree
	CardErrors    []CardinalityError
	OpWarnings    []OpWarning
	MissingStats  []map[string]string
}

// Warning represents a plan-level or operator-level warning element.
type Warning struct {
	Tag        string
	Attrs      map[string]string
	SubWarning []Warning // Nested child elements
}

// Operator represents a node in the execution plan operator tree.
type Operator struct {
	NodeID      int
	PhysicalOp  string
	LogicalOp   string
	EstRows     float64
	ActualRows  int64  // SUM across threads
	ActualExecs int64  // SUM across threads
	ElapsedMs   int64  // MAX across threads
	ObjectInfo  string // e.g. "orders.PK_orders"
	Warnings    []Warning
	Children    []*Operator
}

// CardinalityError reports the estimation error for a single operator.
type CardinalityError struct {
	NodeID     int
	EstRows    float64
	ActualRows int64
	Ratio      float64
	PhysicalOp string
	LogicalOp  string
	Direction  string // "over" or "under"
	ObjectInfo string // e.g. "orders.IX_customer_id"
}

// OpWarning reports a warning associated with a specific operator node.
type OpWarning struct {
	NodeID  int
	Tag     string
	Detail  string
	Attrs   map[string]string
}
