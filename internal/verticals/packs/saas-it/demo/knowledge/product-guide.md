# Northstar Orbit Platform - Product Guide

Synthetic demo document describing the fictional Orbit platform.

## 1 Platform Overview

Orbit provides a unified management console covering compute, data and observability workloads from a single interface. The platform supports multi-tenancy: each tenant's data is isolated in dedicated encrypted storage namespaces with tenant-scoped encryption keys, and tenant data isolation is validated during every release through automated isolation tests. Administrators manage all workloads, policies and users from the console.

## 2 Modules

Orbit is delivered as four modules that share identity, policy and audit services.

### 2.1 Orbit Compute

Orbit Compute provisions and manages virtual machines, containers and serverless functions across supported cloud regions with policy-based placement and automated patching.

### 2.2 Orbit Data Fabric

Orbit Data Fabric provides managed databases, object storage and analytics pipelines. Bulk data import and export are supported in CSV and Parquet formats through the console and the API, with scheduled transfers and checksum validation for every import and export job.

### 2.3 Orbit Observability

Orbit Observability collects metrics, logs and traces with 13 months of retention, alerting and service-level objective tracking.

### 2.4 Orbit Workflow Automation

Orbit Workflow Automation includes a visual designer for building workflows with drag-and-drop steps, approvals and integrations. Workflow definitions are version-controlled: every definition is stored in a Git-backed repository with change history, diff and rollback, and definitions can be promoted between environments.

## 3 APIs and Integrations

Orbit exposes REST and GraphQL APIs secured with OAuth 2.0 authentication (client credentials and authorization code flows). Published rate limits are 1,000 requests per minute per tenant, documented in the developer portal. Integration options include certified connectors for ServiceNow and Jira that synchronize incidents, changes and problem records in both directions, plus connectors for Slack, Microsoft Teams and PagerDuty.

## 4 Reporting and Dashboards

Reporting and dashboard capabilities include configurable dashboards, a library of 60 standard reports and scheduled report distribution by email or to a shared drive. Export formats are PDF, CSV and XLSX. Cloud cost reporting includes chargeback and showback per business unit, cost allocation tags and budget alerts.

## 5 Mobile and Accessibility

Orbit provides a responsive web interface for phones and tablets; there is no separate native app. The interface was audited against WCAG 2.1 AA in March 2026 and a VPAT is available. Keyboard navigation, screen-reader labels and reduced-motion support are included.

## 6 Release Cadence

The release cadence is monthly for platform releases and weekly for minor fixes. Customers preview upcoming features in a preview program and control feature rollouts with tenant-level feature flags, so a feature can be enabled for a pilot group before the whole organization.

## 7 Hosting Model

Hosting model: Orbit runs on AWS and Microsoft Azure as a managed service operated by Northstar. Available regions in the EU are Ireland (Dublin) and Germany (Frankfurt); available regions in North America are the United States (Virginia and Oregon) and Canada (Central). Cloud providers and regions are selected per tenant at onboarding.

## 8 Performance and Scalability

Orbit scales horizontally: stateless services run behind load balancers and add capacity automatically. Performance benchmarks are documented in the Orbit Performance Report 2026: the platform was validated at 5,000 concurrent users per tenant without degradation and at 50,000 concurrent users platform-wide, with median console response under 400 milliseconds.
