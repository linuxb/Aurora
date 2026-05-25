use regex::Regex;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::env;
use std::sync::Arc;
use std::time::Duration;

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
        rule_based_enrich(input)
    }
}

pub struct LlmHttpEnricher {
    endpoint: String,
    timeout_ms: u64,
    strict: bool,
    fallback_rule_based: bool,
}

impl LlmHttpEnricher {
    fn new(endpoint: String, timeout_ms: u64, strict: bool, fallback_rule_based: bool) -> Self {
        Self {
            endpoint,
            timeout_ms,
            strict,
            fallback_rule_based,
        }
    }
}

impl Enricher for LlmHttpEnricher {
    fn enrich(&self, input: EnrichInput) -> EnrichOutput {
        let base = EnrichOutput {
            hard_facts: dedup(input.hard_facts.clone()),
            rels: input.rels.clone(),
        };

        let req = LlmEnrichRequest {
            summary: input.summary.clone(),
            raw_output: input.raw_output.clone(),
            hard_facts: input.hard_facts.clone(),
            rels: input.rels.clone(),
        };

        let client = match Client::builder()
            .timeout(Duration::from_millis(self.timeout_ms))
            .build()
        {
            Ok(v) => v,
            Err(_) => return self.on_failure(input, base),
        };

        let response = client.post(&self.endpoint).json(&req).send();
        let parsed = match response {
            Ok(resp) if resp.status().is_success() => resp.json::<LlmEnrichResponse>().ok(),
            _ => None,
        };

        if let Some(parsed) = parsed {
            let mut hard_facts = base.hard_facts;
            hard_facts.extend(parsed.hard_facts.unwrap_or_default());
            hard_facts = dedup(hard_facts);

            let mut rels = base.rels;
            rels.extend(parsed.rels.unwrap_or_default());
            rels = normalize_rels(rels);

            return EnrichOutput { hard_facts, rels };
        }

        self.on_failure(input, base)
    }
}

impl LlmHttpEnricher {
    fn on_failure(&self, input: EnrichInput, base: EnrichOutput) -> EnrichOutput {
        if self.strict {
            return base;
        }
        if self.fallback_rule_based {
            return rule_based_enrich(input);
        }
        base
    }
}

#[derive(Serialize)]
struct LlmEnrichRequest {
    summary: String,
    raw_output: String,
    hard_facts: Vec<String>,
    rels: Vec<[String; 3]>,
}

#[derive(Deserialize)]
struct LlmEnrichResponse {
    hard_facts: Option<Vec<String>>,
    rels: Option<Vec<[String; 3]>>,
}

pub fn build_enricher_from_env() -> Arc<dyn Enricher> {
    let backend = env::var("POLARIS_ENRICH_BACKEND")
        .unwrap_or_else(|_| "none".to_string())
        .to_lowercase();
    match backend.as_str() {
        "rule_based" => Arc::new(RuleBasedEnricher),
        "llm_http" => {
            let endpoint = env::var("POLARIS_ENRICH_LLM_ENDPOINT")
                .unwrap_or_else(|_| "http://127.0.0.1:8089/v1/enrich".to_string());
            let timeout_ms = parse_positive_u64_env("POLARIS_ENRICH_LLM_TIMEOUT_MS", 1200);
            let strict = parse_bool_env("POLARIS_ENRICH_LLM_STRICT", false);
            let fallback_rule_based = parse_bool_env("POLARIS_ENRICH_LLM_FALLBACK_RULE", true);
            Arc::new(LlmHttpEnricher::new(
                endpoint,
                timeout_ms,
                strict,
                fallback_rule_based,
            ))
        }
        _ => Arc::new(NoopEnricher),
    }
}

fn rule_based_enrich(input: EnrichInput) -> EnrichOutput {
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

    EnrichOutput {
        hard_facts: dedup(hard_facts),
        rels: normalize_rels(rels),
    }
}

fn normalize_rels(rels: Vec<[String; 3]>) -> Vec<[String; 3]> {
    let mut out = Vec::new();
    for rel in rels {
        if rel[0].trim().is_empty() || rel[1].trim().is_empty() || rel[2].trim().is_empty() {
            continue;
        }
        out.push([rel[0].trim().to_string(), rel[1].trim().to_string(), rel[2].trim().to_string()]);
    }
    out
}

fn dedup(items: Vec<String>) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut out = Vec::new();
    for item in items {
        if seen.insert(item.clone()) {
            out.push(item);
        }
    }
    out
}

fn parse_bool_env(key: &str, fallback: bool) -> bool {
    match env::var(key) {
        Ok(v) => matches!(v.trim().to_lowercase().as_str(), "1" | "true" | "yes" | "on"),
        Err(_) => fallback,
    }
}

fn parse_positive_u64_env(key: &str, fallback: u64) -> u64 {
    match env::var(key).ok().and_then(|v| v.parse::<u64>().ok()) {
        Some(v) if v > 0 => v,
        _ => fallback,
    }
}

#[cfg(test)]
mod tests {
    use super::{
        parse_bool_env, parse_positive_u64_env, EnrichInput, Enricher, LlmEnrichResponse,
        RuleBasedEnricher,
    };

    #[test]
    fn rule_based_enricher_should_extract_error_code_and_calls_relation() {
        let enricher = RuleBasedEnricher;
        let out = enricher.enrich(EnrichInput {
            summary: "ServiceA calls ServiceB during retry".to_string(),
            raw_output: "received ERR_TIMEOUT from upstream".to_string(),
            hard_facts: vec![],
            rels: vec![],
        });
        assert!(out.hard_facts.iter().any(|v| v == "ERROR_CODE=ERR_TIMEOUT"));
        assert!(out.rels.iter().any(|r| {
            r[0].eq_ignore_ascii_case("ServiceA")
                && r[1].eq_ignore_ascii_case("ServiceB")
                && r[2] == "calls"
        }));
    }

    #[test]
    fn parse_env_helpers_should_fallback() {
        assert_eq!(parse_positive_u64_env("POLARIS_TEST_ENRICH_TIMEOUT", 99), 99);
        assert!(!parse_bool_env("POLARIS_TEST_ENRICH_BOOL", false));
    }

    #[test]
    fn llm_response_shape_should_deserialize() {
        let raw = r#"{"hard_facts":["ERROR_CODE=ERR_IO"],"rels":[["A","B","calls"]]}"#;
        let parsed: LlmEnrichResponse = serde_json::from_str(raw).expect("parse response");
        assert_eq!(parsed.hard_facts.unwrap_or_default().len(), 1);
        assert_eq!(parsed.rels.unwrap_or_default().len(), 1);
    }
}
