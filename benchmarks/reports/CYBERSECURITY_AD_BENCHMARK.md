# Active Directory (BloodHound) Enterprise Cybersecurity Benchmark

Evaluation of **GraphRAG (`cypher-mcp`)** vs **Plain Vector RAG** across the official **Neo4j BloodHound Active Directory** corporate dataset (953 enterprise nodes, 4,698 relationships).

## Benchmark Results Summary

| Task | Model / Mode | Fact Recall | Steps (Turns) | Tokens | Duration |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Privilege Escalation & ACL Attack Path (3-Hop Transitive)** | Plain Vector RAG | **0.0%** | 8 | 235,585 | 27.62s |
| | **GraphRAG (`cypher-mcp`)** | **0.0%** | **10** | **491,392** | **26.95s** |
| **Critical Credential Dumping Risk (Legacy OS with DA Sessions)** | Plain Vector RAG | **0.0%** | 10 | 310,120 | 25.31s |
| | **GraphRAG (`cypher-mcp`)** | **100.0%** | **2** | **5,348** | **12.49s** |
| **Exact Administrative Blast Radius (Group-Inherited Admin Rights)** | Plain Vector RAG | **100.0%** | 6 | 56,576 | 23.87s |
| | **GraphRAG (`cypher-mcp`)** | **100.0%** | **5** | **8,670** | **13.81s** |


## Task Details & Insights

### Privilege Escalation & ACL Attack Path (3-Hop Transitive)

**Plain RAG Final Output:**

```text
Local`
  - `MEMBER_OF` -> `IT00195@TestCompany.Local`
  - `MEMBER_OF` -> `IT00228@TestCompany.Local`
  - `MEMBER_OF` -> `IT00147@TestCompany.Local`
  Not DOMAIN ADMINS...
```

**GraphRAG Final Output:**

```text

```

---

### Critical Credential Dumping Risk (Legacy OS with DA Sessions)

**Plain RAG Final Output:**

```text
...
```

**GraphRAG Final Output:**

```text
Based on the Active Directory graph audit, here are all the computers running legacy operating systems (**Windows 7** or **Windows Server 2008**) with an active session belonging to a member of **DOMAIN ADMINS@TestCompany.Local**:

| Computer Name | Operating System | Active Domain Admin Session |
| :--- | :--- | :--- |
| `COMP00017.TestCompany.Local` | Windows 7 | MalkaMclaney272@TestCompany.Local |
| `COMP00041.TestCompany.Local` | Windows 7 | PiedadFlatley255@TestCompany.Local |
| `COMP00045.TestCompany.Local` | Windows Server 2008 | PingFrankel165@TestCompany.Local |
| `COMP00051.TestCompany.Local` | Windows 7 | ReaHardung258@TestCompany.Local |
| `COMP00080.TestCompany.Local` | Windows 7 | MignonSliwinski33@TestCompany.Local |
| `COMP00083.TestCompany.Local` | Windows Server 2008 | VirgenAurand248@TestCompany.Local |
| `COMP00117.TestCompany.Local` | Windows 7 | GlynisMccorkindale95@TestCompany.Local |
| `COMP00159.TestCompany.Local` | Windows 7 | KeniaKovar17@TestCompany.Local |
| `COMP00208.TestCompany.Local` | Windows 7 | KeniaKovar17@TestCompany.Local |
| `COMP00218.TestCompany.Local` | Windows 7 | KarineBallengee58@TestCompany.Local |
| `COMP00254.TestCompany.Local` | Windows Server 2008 | HilaryOlivia226@TestCompany.Local |
| `COMP00265.TestCompany.Local` | Windows Server 2008 | ReaHardung258@TestCompany.Local |
| `COMP00274.TestCompany.Local` | Windows Server 2008 | MaxCullop156@TestCompany.Local |

### Summary
- **Total computers identified:** 13
  - **Windows 7:** 8 computers
  - **Windows Server 2008:** 5 computers
```

---

### Exact Administrative Blast Radius (Group-Inherited Admin Rights)

**Plain RAG Final Output:**

```text
Based on the Active Directory domain audit, user **`PedroReif62@TestCompany.Local`** possesses local administrative privileges (**`ADMIN_TO`**) on **4** computers, all inherited through security group memberships (no direct `ADMIN_TO` relationships exist):

### Group Memberships & Inherited Administrative Access:
1. **`IT00147@TestCompany.Local`**
   - **`COMP00012.TestCompany.Local`**
2. **`IT00195@TestCompany.Local`**
   - **`COMP00191.TestCompany.Local`**
3. **`IT00228@TestCompany.Local`**
   - **`COMP00081.TestCompany.Local`**
   - **`COMP00219.TestCompany.Local`**

*(Note: Direct outbound privileges to `COMP00106.TestCompany.Local` and `COMP00218.TestCompany.Local` are `EXECUTE_DCOM`, not `ADMIN_TO`.)*

---

### Summary:
- **Exact Count:** 4
- **Computer Names:**
  - `COMP00012.TestCompany.Local`
  - `COMP00081.TestCompany.Local`
  - `COMP00191.TestCompany.Local`
  - `COMP00219.TestCompany.Local`...
```

**GraphRAG Final Output:**

```text
Based on the Active Directory graph audit for user **PedroReif62@TestCompany.Local**:

### Direct Local Administrative Privileges
* **Direct (`ADMIN_TO`):** None (0 computers)

### Inherited Local Administrative Privileges
The user inherits local administrative privileges across **4** computers via group memberships:

1. **COMP00012.TestCompany.Local** (via group `IT00147@TestCompany.Local`)
2. **COMP00081.TestCompany.Local** (via group `IT00228@TestCompany.Local`)
3. **COMP00191.TestCompany.Local** (via group `IT00195@TestCompany.Local`)
4. **COMP00219.TestCompany.Local** (via group `IT00228@TestCompany.Local`)

---

### Summary
* **Exact Count:** 4
* **Computer Names:**
  - `COMP00012.TestCompany.Local`
  - `COMP00081.TestCompany.Local`
  - `COMP00191.TestCompany.Local`
  - `COMP00219.TestCompany.Local`
```

---

