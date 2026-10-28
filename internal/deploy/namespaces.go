package deploy

// SharedComponentNamespace is the canonical namespace name where the F5
// Cluster-Wide Controller (CWC), License CR, observer, and other
// release-wide singletons live in BNK 2.3+. F5's public docs use
// `f5-cne-core`; this constant centralizes it so a future override
// flag has one place to change.
const SharedComponentNamespace = "f5-cne-core"
