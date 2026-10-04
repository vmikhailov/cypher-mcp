# Active Directory (BloodHound) Enterprise Cybersecurity Benchmark

Evaluation of **GraphRAG (`cypher-mcp`)** vs **Plain Vector RAG** across the official **Neo4j BloodHound Active Directory** corporate dataset (953 enterprise nodes, 4,698 relationships).

## Benchmark Results Summary

| Task | Model / Mode | Fact Recall | Steps (Turns) | Tokens | Duration |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Privilege Escalation & ACL Attack Path (3-Hop Transitive)** | Plain Vector RAG | **0.0%** | 10 | 275,945 | 20.60s |
| | **GraphRAG (`cypher-mcp`)** | **100.0%** | **4** | **11,801** | **10.30s** |
| **Critical Credential Dumping Risk (Legacy OS with DA Sessions)** | Plain Vector RAG | **0.0%** | 10 | 320,133 | 25.51s |
| | **GraphRAG (`cypher-mcp`)** | **100.0%** | **2** | **5,476** | **13.47s** |
| **Exact Administrative Blast Radius (Group-Inherited Admin Rights)** | Plain Vector RAG | **100.0%** | 5 | 37,935 | 19.88s |
| | **GraphRAG (`cypher-mcp`)** | **100.0%** | **5** | **10,654** | **13.60s** |


## Task Details & Insights

### Privilege Escalation & ACL Attack Path (3-Hop Transitive)

**Plain RAG Final Output:**

```text
...
```

**GraphRAG Final Output:**

```text
Based on the graph queries and Active Directory audit, here are the findings for the indirect privilege escalation path to Domain Admins:

### Privilege Escalation Path Details
* **Non-Admin Users:**
  * `NannieDeltoro01@TestCompany.Local`
  * `PedroReif62@TestCompany.Local`
  * `ShelaRebolloso75@TestCompany.Local`
  * `ChinaBracey203@TestCompany.Local`
* **Intermediate Group:** `IT00195@TestCompany.Local` (which these users are members of)
* **Permission Type:** `GENERIC_WRITE`
* **Target Domain Admin Account:** `DanielleGallery238@TestCompany.Local` (member of `DOMAIN ADMINS@TestCompany.Local`)

*(Note: `ShoshanaDeahl233@TestCompany.Local` is also a member of `IT00195@TestCompany.Local`, but is already a Domain Admin).*
```

---

### Critical Credential Dumping Risk (Legacy OS with DA Sessions)

**Plain RAG Final Output:**

```text
Based on an audit of the group **DOMAIN ADMINS@TestCompany.Local**, there are 15 members in the group:
- `VirgenAurand248@TestCompany.Local`
- `PiedadFlatley255@TestCompany.Local`
- `MignonSliwinski33@TestCompany.Local`
- `MaxCullop156...
```

**GraphRAG Final Output:**

```text
Based on the Active Directory graph audit, the following **13 computers** are running legacy operating systems (`Windows 7` or `Windows Server 2008`) and currently host an active session (`HAS_SESSION`) for a member of `DOMAIN ADMINS@TestCompany.Local`:

| Computer Name | Operating System | Active Domain Admin User |
| :--- | :--- | :--- |
| **COMP00017.TestCompany.Local** | Windows 7 | MalkaMclaney272@TestCompany.Local |
| **COMP00041.TestCompany.Local** | Windows 7 | PiedadFlatley255@TestCompany.Local |
| **COMP00045.TestCompany.Local** | Windows Server 2008 | PingFrankel165@TestCompany.Local |
| **COMP00051.TestCompany.Local** | Windows 7 | ReaHardung258@TestCompany.Local |
| **COMP00080.TestCompany.Local** | Windows 7 | MignonSliwinski33@TestCompany.Local |
| **COMP00083.TestCompany.Local** | Windows Server 2008 | VirgenAurand248@TestCompany.Local |
| **COMP00117.TestCompany.Local** | Windows 7 | GlynisMccorkindale95@TestCompany.Local |
| **COMP00159.TestCompany.Local** | Windows 7 | KeniaKovar17@TestCompany.Local |
| **COMP00208.TestCompany.Local** | Windows 7 | KeniaKovar17@TestCompany.Local |
| **COMP00218.TestCompany.Local** | Windows 7 | KarineBallengee58@TestCompany.Local |
| **COMP00254.TestCompany.Local** | Windows Server 2008 | HilaryOlivia226@TestCompany.Local |
| **COMP00265.TestCompany.Local** | Windows Server 2008 | ReaHardung258@TestCompany.Local |
| **COMP00274.TestCompany.Local** | Windows Server 2008 | MaxCullop156@TestCompany.Local |

---

### Security Impact
These systems lack modern credential-protection safeguards (e.g., Credential Guard, LSA Protection/RunAsPPL enabled by default, restricted admin modes), making them prime targets for LSASS credential harvesting (e.g., via Mimikatz) to compromise Domain Admin credentials. Immediate remediation should include terminating active privileged sessions, enforcing tiering restrictions, and upgrading or decommissioning these legacy platforms.
```

---

### Exact Administrative Blast Radius (Group-Inherited Admin Rights)

**Plain RAG Final Output:**

```text
Based on the Active Directory domain audit, here is the breakdown of local administrative privileges (`ADMIN_TO`) for **PedroReif62@TestCompany.Local**:

### Direct Privileges
* The user **`PedroReif62@TestCompany.Local`** does not hold any direct `ADMIN_TO` permissions on any domain computers.

---

### Inherited Privileges via Group Membership
The user is a member of the following groups granting `ADMIN_TO` permissions:

1. **`IT00147@TestCompany.Local`**
   * `ADMIN_TO` -> **`COMP00012.TestCompany.Local`**
2. **`IT00195@TestCompany.Local`**
   * `ADMIN_TO` -> **`COMP00191.TestCompany.Local`**
3. **`IT00228@TestCompany.Local`**
   * `ADMIN_TO` -> **`COMP00081.TestCompany.Local`**
   * `ADMIN_TO` -> **`COMP00219.TestCompany.Local`**

*(Note: `DOMAIN USERS@TestCompany.Local` possesses no outbound `ADMIN_TO` privileges.)*

---

### Summary
* **Exact Count:** **4** computers
* **Computer Names:**
  1. `COMP00012.TestCompany.Local`
  2. `COMP00081.TestCompany.Local`
  3. `COMP00191.TestCo...
```

**GraphRAG Final Output:**

```text
An audit of the local administrative privileges (`ADMIN_TO`) for user **`PedroReif62@TestCompany.Local`** indicates that the user holds administrative access to a total of **4** computers, all inherited through group memberships (with 0 direct assignments).

### **Summary of Privileges**
* **Direct Administrative Access:** 0 computers
* **Group-Inherited Administrative Access:** 4 computers
* **Total Blast Radius:** **4 computers**

---

### **List of Computers**

| Computer Name | Inherited Via Group |
| :--- | :--- |
| **`COMP00012.TestCompany.Local`** | `IT00147@TestCompany.Local` |
| **`COMP00081.TestCompany.Local`** | `IT00228@TestCompany.Local` |
| **`COMP00191.TestCompany.Local`** | `IT00195@TestCompany.Local` |
| **`COMP00219.TestCompany.Local`** | `IT00228@TestCompany.Local` |
```

---

