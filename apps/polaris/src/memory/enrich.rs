use regex::Regex;
use std::env;
use std::sync::Arc;

pub struct EnrichInput {
    pub summary: String,
    pub raw_output: String,
    pub hard_facts: Vec<String>,
    pub rels: Vec<[String; 3]>,
}

pub struct EnrichOutput {
    pub hard_facts: Vec<String>,
    pub rels: Vec<[String; 3]>,
}

pub trait Enricher: Send + Sync {
    fn enrich(&self, input: EnrichInput) -> EnrichOutput;
}

pub struct NoopEnricher;

impl Enricher for NoopEnricher {
    fn enrich(&self, input: EnrichInput) -> EnrichOutput {
        EnrichOutput {
            hard_facts: input.hard_facts,
            rels: input.rels,
        }
    }
}

pub struct RuleBasedEnricher;

impl Enricher for RuleBasedEnricher {
    fn enrich(&self, input: EnrichInput) -> EnrichOutput {
        let mut hard_facts = input.hard_facts;
        let mut rels = input.rels;
        let summary = input.summary;
        let raw = input.raw_output;

        let err_code_re = Regex::new(r"\b(ERR_[A-Z0-9_]+)\b").expect("invalid regex");
        for cap in err_code_re.find_iter(&raw) {
            let fact = format!("ERROR_CODE={}", cap.as_str());
            if !hard_facts.contains(&fact) {
                hard_facts.push(fact);
            }
        }

        if rels.is_empty() {
            let calls_re = Regex::new(r"(?i)\b([A-Za-z][A-Za-z0-9_\-]{2,})\s+calls\s+([A-Za-z][A-Za-z0-9_\-]{2,})\b")
                .expect("invalid regex");
            if let Some(cap) = calls_re.captures(&summary)
                && let (Some(a), Some(b)) = (cap.get(1), cap.get(2))
            {
                rels.push([a.as_str().to_string(), b.as_str().to_string(), "calls".to_string()]);
            }
        }

        EnrichOutput { hard_facts, rels }
    }
}

pub fn build_enricher_from_env() -> Arc<dyn Enricher> {
    let backend = env::var("POLARIS_ENRICH_BACKEND")
        .unwrap_or_else(|_| "none".to_string())
        .to_lowercase();
    match backend.as_str() {
        "rule_based" => Arc::new(RuleBasedEnricher),
        _ => Arc::new(NoopEnricher),
    }
}

#[cfg(test)]
mod tests {
    use super::{EnrichInput, Enricher, RuleBasedEnricher};

    #[test]
    fn rule_based_enricher_should_extract_error_code_and_calls_relation() {
        let enricher = RuleBasedEnricher;
        let out = enricher.enrich(EnrichInput {
            summary: "ServiceA calls ServiceB during retry".to_string(),
            raw_output: "received ERR_TIMEOUT from upstream".to_string(),
            hard_facts: vec![],
            rels: vec![],
        });
        assert!(out
            .hard_facts
            .iter()
            .any(|v| v == "ERROR_CODE=ERR_TIMEOUT"));
        assert!(out.rels.iter().any(|r| {
            r[0].eq_ignore_ascii_case("ServiceA") && r[1].eq_ignore_ascii_case("ServiceB") && r[2] == "calls"
        }));
    }
}
