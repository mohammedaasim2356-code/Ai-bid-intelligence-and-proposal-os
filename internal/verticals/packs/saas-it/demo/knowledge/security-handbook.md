# Security Controls Handbook v2.1

Synthetic demo document. This handbook describes the security controls operated by Northstar Cloud Systems for the Orbit platform.

## 4 Technical Controls

Section 4 documents the technical controls that customers most often ask about in security questionnaires.

### 4.1 Access Control

Single sign-on is provided through SAML 2.0 with SCIM provisioning for Okta, Microsoft Entra ID and Ping Identity. Multi-factor authentication (MFA) is enforced for all administrative users and can be enforced for all users by policy. Role-based access control supports custom roles, least-privilege defaults and delegated administration so business units can manage their own users. Access reviews run quarterly.

### 4.2 Encryption

All customer data is encrypted at rest using AES-256 and in transit using TLS 1.2 or higher (TLS 1.3 preferred). Encryption key management: keys are held in AWS KMS or Azure Key Vault; customer-managed keys (CMK) are supported so customers control their own encryption keys; key rotation frequency is every 90 days for data keys and annually for root keys.

### 4.3 Vulnerability Management

Vulnerability scans run weekly on all production systems and daily on container images. Independent third-party penetration tests are performed annually (annual third-party penetration testing) and after major releases; an executive summary of the most recent penetration test is shared on request under NDA. Critical vulnerabilities are remediated within 7 days.

### 4.4 Incident Response

The security incident response process is run by a 24x7 security operations team following documented playbooks (detect, contain, eradicate, recover, review). Customer notification timeline: affected customers are notified within 24 hours of a confirmed security incident, with a written post-incident report within 5 business days.

### 4.5 Logging and Monitoring

Audit logs of all administrative actions are retained for 365 days (12 months) and are available to customers through the console and API. Logging and monitoring capabilities include SIEM integration with Splunk, Microsoft Sentinel and any syslog or webhook destination; log retention periods can be extended to 24 months on request for an additional fee.

### 4.6 Backup and Disaster Recovery

Disaster recovery capabilities: data is replicated across availability zones with a recovery point objective (RPO) of 15 minutes and a recovery time objective (RTO) of 4 hours. Failover testing frequency: full regional failover tests are performed twice per year. A documented business continuity plan is reviewed and tested annually.

### 4.7 Secure Development

Secure software development practices include mandatory peer code review for every change, automated dependency scanning, static application security testing (SAST) and dynamic testing (DAST) in the delivery pipeline, and annual secure coding training for engineers.

### 4.8 Physical Security

Orbit runs in hyperscaler data centers operated by AWS and Microsoft Azure. Physical security (perimeter control, biometric access, CCTV) is inherited from those providers and covered by their independent attestations; Northstar staff have no physical access to the data centers.

### 4.9 Personnel Security

Background checks are performed on all employees before hire, and employees receive security awareness training at onboarding and annually thereafter, with phishing simulations each quarter.
