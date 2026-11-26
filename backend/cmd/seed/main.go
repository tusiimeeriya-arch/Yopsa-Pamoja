// Command seed populates the development database with the Yopsa Pamoja demo
// portfolio: three branches, six groups, their members, five months of meeting
// registers and savings entries, borrowing applications across the workflow and
// follow-up tasks. It is idempotent: if any user exists it does nothing.
//
// It runs as the migration (owner) role because it writes identity rows the API
// role may not insert. Run it only against a development or staging database.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yopsa/pamoja/backend/internal/auth"
	"github.com/yopsa/pamoja/backend/internal/platform/keyring"
)

const demoPassword = "Pamoja#2026"

type demoBranch struct {
	Key, Name, Code, District string
}

type demoUser struct {
	Name, Phone, Role string
	Scope             string // le, branch, group
	Branch, Group     string
}

type demoGroup struct {
	Key, Name, Type, Status, Branch, Village, SubCounty, District string
	Day, Frequency                                                string
	FormedOn                                                      string
	Members                                                       int
	Savings                                                       int64
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	dbURL := os.Getenv("SEED_DATABASE_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		return fmt.Errorf("SEED_DATABASE_URL or DATABASE_URL is required")
	}
	keys, err := keyring.NewLocal(os.Getenv("PAMOJA_MASTER_KEY"), os.Getenv("PAMOJA_LOOKUP_KEY"))
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var existingUsers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pamoja.app_user`).Scan(&existingUsers); err != nil {
		return fmt.Errorf("read app_user (has 000003 been applied?): %w", err)
	}
	if existingUsers > 0 {
		log.Printf("seed: %d users already exist, nothing to do", existingUsers)
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var legalEntityID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO pamoja.legal_entity (name, country, currency)
		 VALUES ('Yopsa Microfinance Uganda', 'Uganda', 'UGX') RETURNING id::text`).
		Scan(&legalEntityID); err != nil {
		return err
	}

	branches := []demoBranch{
		{Key: "rwimi", Name: "Rwimi Branch", Code: "RWM", District: "Bunyangabu"},
		{Key: "kasese", Name: "Kasese Town Branch", Code: "KSE", District: "Kasese"},
		{Key: "fortportal", Name: "Fort Portal Branch", Code: "FPT", District: "Kabarole"},
	}
	branchIDs := map[string]string{}
	for _, b := range branches {
		var id string
		if err := tx.QueryRow(ctx,
			`INSERT INTO pamoja.branch (legal_entity_id, name, code, district)
			 VALUES ($1::uuid, $2, $3, $4) RETURNING id::text`,
			legalEntityID, b.Name, b.Code, b.District).Scan(&id); err != nil {
			return err
		}
		branchIDs[b.Key] = id
	}

	groups := []demoGroup{
		{Key: "twezimbe", Name: "Twezimbe Women's Group", Type: "vsla", Status: "active", Branch: "rwimi",
			Village: "Rwimi Central", SubCounty: "Rwimi", District: "Bunyangabu", Day: "Sunday",
			Frequency: "weekly", FormedOn: "2023-02-12", Members: 14, Savings: 10000},
		{Key: "kyabazinga", Name: "Kyabazinga Investment Club", Type: "investment_club", Status: "active", Branch: "rwimi",
			Village: "Hima", SubCounty: "Kitswamba", District: "Kasese", Day: "Saturday",
			Frequency: "weekly", FormedOn: "2022-08-06", Members: 12, Savings: 15000},
		{Key: "bwera", Name: "Bwera Boda Riders SACCO", Type: "sacco", Status: "active", Branch: "kasese",
			Village: "Bwera", SubCounty: "Mpondwe-Lhubiriha", District: "Kasese", Day: "Monday",
			Frequency: "weekly", FormedOn: "2021-11-15", Members: 16, Savings: 12000},
		{Key: "nyakato", Name: "Nyakato Market Vendors", Type: "association", Status: "active", Branch: "fortportal",
			Village: "Nyakato", SubCounty: "Central", District: "Kabarole", Day: "Wednesday",
			Frequency: "weekly", FormedOn: "2024-01-24", Members: 11, Savings: 6000},
		{Key: "kigarama", Name: "Kigarama Coffee Cooperative", Type: "cooperative", Status: "pending_activation", Branch: "rwimi",
			Village: "Kigarama", SubCounty: "Maliba", District: "Kasese", Day: "Thursday",
			Frequency: "fortnightly", FormedOn: "2026-10-08", Members: 9, Savings: 8000},
		{Key: "bunyonyi", Name: "Bunyonyi Savings Group", Type: "vsla", Status: "active", Branch: "fortportal",
			Village: "Bunyonyi", SubCounty: "Bubare", District: "Kabale", Day: "Sunday",
			Frequency: "weekly", FormedOn: "2023-06-18", Members: 10, Savings: 7000},
	}

	groupIDs := map[string]string{}
	for _, g := range groups {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO pamoja.grp (branch_id, code, name, group_type, status, formed_on,
				meeting_day, meeting_frequency, village, sub_county, district, cycle_start, cycle_end)
			VALUES ($1::uuid, $2, $3, $4, $5, $6::date, $7, $8, $9, $10, $11, '2026-01-01', '2026-12-31')
			RETURNING id::text`,
			branchIDs[g.Branch], "YP-"+fmt.Sprintf("%04d", len(groupIDs)+1), g.Name, g.Type, g.Status,
			g.FormedOn, g.Day, g.Frequency, g.Village, g.SubCounty, g.District).Scan(&id); err != nil {
			return err
		}
		groupIDs[g.Key] = id
	}

	users := []demoUser{
		{Name: "Grace Namusoke", Phone: "+256700000001", Role: "management", Scope: "le"},
		{Name: "Daniel Kato", Phone: "+256700000002", Role: "field_officer", Scope: "branch", Branch: "rwimi"},
		{Name: "Sarah Auma", Phone: "+256700000003", Role: "verifier", Scope: "branch", Branch: "rwimi"},
		{Name: "Robert Mugisha", Phone: "+256700000004", Role: "credit_approver", Scope: "branch", Branch: "rwimi"},
		{Name: "Esther Nabirye", Phone: "+256700000005", Role: "treasurer", Scope: "group", Group: "twezimbe"},
		{Name: "Moses Okello", Phone: "+256700000006", Role: "it_admin", Scope: "le"},
		{Name: "Patricia Atim", Phone: "+256700000007", Role: "auditor", Scope: "le"},
		{Name: "Joan Kembabazi", Phone: "+256700000008", Role: "secretary", Scope: "group", Group: "twezimbe"},
		{Name: "Peter Byaruhanga", Phone: "+256700000009", Role: "chairperson", Scope: "group", Group: "twezimbe"},
	}

	userIDs := map[string]string{}
	byRole := map[string]string{}
	for _, u := range users {
		passwordHash, params, err := auth.HashPassword(demoPassword)
		if err != nil {
			return err
		}
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return err
		}
		sealed, err := keys.Encrypt([]byte(u.Phone))
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO pamoja.app_user (full_name, phone_hash, phone_enc, phone_masked, is_staff, mfa_required, status)
			VALUES ($1, $2, $3, $4, true, false, 'active') RETURNING id::text`,
			u.Name, keys.LookupHash(auth.NormalizePhone(u.Phone)), sealed, maskPhone(u.Phone)).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO pamoja.user_credential (user_id, password_hash, argon2_params)
			 VALUES ($1::uuid, $2, $3::jsonb)`, id, passwordHash, string(paramsJSON)); err != nil {
			return err
		}
		userIDs[u.Role] = id
		byRole[u.Role] = id
	}

	for _, u := range users {
		var legalEntity, branch, group *string
		switch u.Scope {
		case "le":
			legalEntity = &legalEntityID
		case "branch":
			id := branchIDs[u.Branch]
			branch = &id
		case "group":
			id := groupIDs[u.Group]
			group = &id
		}
		grantedBy := byRole["it_admin"]
		if _, err := tx.Exec(ctx, `
			INSERT INTO pamoja.role_grant (user_id, role, legal_entity_id, branch_id, group_id, granted_by, approved_by, status)
			VALUES ($1::uuid, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $6::uuid, 'active')`,
			userIDs[u.Role], u.Role, legalEntity, branch, group, grantedBy); err != nil {
			return err
		}
	}

	firstNames := []string{"Aisha", "Brian", "Christine", "David", "Esther", "Francis", "Grace", "Hakim",
		"Irene", "Joseph", "Kevin", "Lydia", "Moses", "Naomi", "Patrick", "Rehema", "Samuel", "Teddy",
		"Uganda", "Violet", "Wilson", "Yasmin", "Zawadi", "Annet", "Bosco", "Consolate", "Doreen", "Edwin"}
	lastNames := []string{"Nabirye", "Okello", "Mugisha", "Auma", "Kato", "Namusoke", "Byaruhanga", "Atim",
		"Kembabazi", "Tumusiime", "Kobusingye", "Wasswa", "Nakato", "Ssemakula", "Achieng", "Baluku",
		"Kyomuhendo", "Ochieng", "Ninsiima", "Mwesigwa"}

	memberIDs := map[string][]string{}
	now := time.Now()
	for gi, g := range groups {
		gid := groupIDs[g.Key]
		ids := make([]string, 0, g.Members)
		for i := 0; i < g.Members; i++ {
			role := "member"
			switch i {
			case 0:
				role = "chairperson"
			case 1:
				role = "secretary"
			case 2:
				role = "treasurer"
			}
			gender := "female"
			if (gi+i)%2 == 1 {
				gender = "male"
			}
			name := firstNames[(gi*5+i*3)%len(firstNames)] + " " + lastNames[(gi*7+i*2)%len(lastNames)]
			phone := fmt.Sprintf("+2567%02d%06d", 10+gi, (i+1)*137+gi*11)
			var id string
			if err := tx.QueryRow(ctx, `
				INSERT INTO pamoja.group_member (group_id, member_no, full_name, phone_masked, phone_hash, gender, role_in_group, status, joined_on)
				VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'active', $8::date)
				RETURNING id::text`,
				gid, fmt.Sprintf("M-%03d", i+1), name, maskPhone(phone),
				keys.LookupHash(auth.NormalizePhone(phone)), gender, role, g.FormedOn).Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		memberIDs[g.Key] = ids
	}

	treasurer := userIDs["treasurer"]
	verifier := userIDs["verifier"]
	secretary := userIDs["secretary"]

	meetingsPerGroup := 18
	for gi, g := range groups {
		if g.Status != "active" {
			continue
		}
		gid := groupIDs[g.Key]
		ids := memberIDs[g.Key]
		for m := 0; m < meetingsPerGroup; m++ {
			heldOn := now.AddDate(0, 0, -7*(meetingsPerGroup-1-m))
			status := "closed"
			if g.Key == "twezimbe" && m == meetingsPerGroup-1 {
				status = "draft"
			}
			location := g.Village + " Community Hall"
			agenda := "Opening prayer; savings collection; loan review; AOB"
			minutes := ""
			if status == "closed" {
				minutes = "The chairperson called the meeting to order. Members made their weekly savings contributions. " +
					"The treasurer presented the cash book and the secretary read the previous minutes, which were confirmed."
			}
			var meetingID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO pamoja.meeting (group_id, meeting_no, held_on, meeting_type, status, location, agenda, minutes, created_by, closed_at)
				VALUES ($1::uuid, $2, $3::date, 'regular', $4, $5, $6, $7, $8::uuid, $9)
				RETURNING id::text`,
				gid, m+1, heldOn.Format("2006-01-02"), status, location, agenda, minutes, secretary,
				closedAt(status, heldOn)).Scan(&meetingID); err != nil {
				return err
			}

			present := 0
			for i, mid := range ids {
				attendance := "present"
				switch (gi + i + m) % 17 {
				case 0:
					attendance = "absent"
				case 1:
					attendance = "excused"
				case 2, 3:
					attendance = "late"
				}
				if attendance == "present" || attendance == "late" {
					present++
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO pamoja.attendance (meeting_id, member_id, status)
					VALUES ($1::uuid, $2::uuid, $3)`, meetingID, mid, attendance); err != nil {
					return err
				}
			}

			if status != "closed" || present == 0 {
				continue
			}
			for i, mid := range ids {
				amount := g.Savings + int64((i%3)*2000)
				contributionStatus := "verified"
				if m == meetingsPerGroup-2 {
					contributionStatus = "submitted"
				}
				if g.Key == "bwera" && m == meetingsPerGroup-4 && i == 4 {
					contributionStatus = "returned"
				}
				method := "cash"
				if i%2 == 0 {
					method = "mobile_money"
				}
				var verifiedBy any
				var verifiedAt any
				if contributionStatus == "verified" {
					verifiedBy = verifier
					verifiedAt = heldOn
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO pamoja.contribution (group_id, member_id, meeting_id, kind, amount_ugx, method, reference, status, recorded_by, verified_by, occurred_on, verified_at)
					VALUES ($1::uuid, $2::uuid, $3::uuid, 'savings', $4, $5, $6, $7, $8::uuid, $9::uuid, $10::date, $11)`,
					gid, mid, meetingID, amount, method,
					fmt.Sprintf("%s-%03d", g.Key[:3], m*100+i), contributionStatus, treasurer, verifiedBy,
					heldOn.Format("2006-01-02"), verifiedAt); err != nil {
					return err
				}
				if m == 0 {
					if _, err := tx.Exec(ctx, `
						INSERT INTO pamoja.contribution (group_id, member_id, meeting_id, kind, amount_ugx, method, status, recorded_by, verified_by, occurred_on, verified_at)
						VALUES ($1::uuid, $2::uuid, $3::uuid, 'shares', 20000, 'bank', 'verified', $4::uuid, $5::uuid, $6::date, $6::date)`,
						gid, mid, meetingID, treasurer, verifier, heldOn.Format("2006-01-02")); err != nil {
						return err
					}
				}
				if m%4 == 0 {
					if _, err := tx.Exec(ctx, `
						INSERT INTO pamoja.contribution (group_id, member_id, meeting_id, kind, amount_ugx, method, status, recorded_by, verified_by, occurred_on, verified_at)
						VALUES ($1::uuid, $2::uuid, $3::uuid, 'social_fund', 1000, 'cash', 'verified', $4::uuid, $5::uuid, $6::date, $6::date)`,
						gid, mid, meetingID, treasurer, verifier, heldOn.Format("2006-01-02")); err != nil {
						return err
					}
				}
			}
		}
	}

	fieldOfficer := userIDs["field_officer"]
	approver := userIDs["credit_approver"]
	type demoLoan struct {
		Group, Status string
		Amount        int64
		Purpose       string
		Term          int
		Appraisal     string
		Decision      string
	}
	loans := []demoLoan{
		{Group: "twezimbe", Status: "submitted", Amount: 5000000, Purpose: "Stock for a member's produce trading", Term: 9},
		{Group: "bwera", Status: "appraised", Amount: 12000000, Purpose: "Boda spares and two replacement motorcycles", Term: 12,
			Appraisal: "Cash book, attendance and savings history reviewed; repayment capacity is adequate."},
		{Group: "nyakato", Status: "declined", Amount: 4000000, Purpose: "Market stall expansion", Term: 6,
			Appraisal: "Savings history is thin for the requested amount.", Decision: "Declined at this cycle; reapply after three months of consistent savings."},
		{Group: "kyabazinga", Status: "disbursed", Amount: 6000000, Purpose: "Irrigation equipment for member gardens", Term: 12,
			Appraisal: "Strong group record and consistent attendance.", Decision: "Approved for the full amount."},
		{Group: "twezimbe", Status: "approved", Amount: 8000000, Purpose: "School fees bridge for members' children", Term: 6,
			Appraisal: "Group has 92% attendance and a clean repayment record.", Decision: "Approved; awaiting disbursement confirmation."},
		{Group: "bunyonyi", Status: "disbursed", Amount: 3000000, Purpose: "Fishing nets and boat repair", Term: 6,
			Appraisal: "Seasonal income is sufficient.", Decision: "Approved."},
	}
	for _, l := range loans {
		gid := groupIDs[l.Group]
		applicant := memberIDs[l.Group][0]
		var appraisedBy, approvedBy any
		var appraisalNote, decisionNote string
		var decidedOn, disbursedOn any
		appraisalNote = l.Appraisal
		decisionNote = l.Decision
		if l.Status != "submitted" {
			appraisedBy = fieldOfficer
		}
		if l.Status == "approved" || l.Status == "declined" || l.Status == "disbursed" {
			approvedBy = approver
			decidedOn = now.AddDate(0, 0, -9).Format("2006-01-02")
		}
		if l.Status == "disbursed" {
			disbursedOn = now.AddDate(0, 0, -4).Format("2006-01-02")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO pamoja.loan_application (group_id, applicant_member_id, amount_ugx, purpose, term_months, status,
				appraisal_note, decision_note, appraised_by, approved_by, submitted_on, decided_on, disbursed_on, created_by)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9::uuid, $10::uuid, current_date - 21, $11::date, $12::date, $13::uuid)`,
			gid, applicant, l.Amount, l.Purpose, l.Term, l.Status, appraisalNote, decisionNote,
			appraisedBy, approvedBy, decidedOn, disbursedOn, approver); err != nil {
			return err
		}
	}

	type demoTask struct {
		Group, Title, Detail, Priority, Status string
		DueIn                                  int
		Assignee                               string
	}
	tasks := []demoTask{
		{Group: "twezimbe", Title: "Collect the missing attendance sheet", Detail: "The register for the last meeting is still open; close it before the next cycle report.", Priority: "high", Status: "open", DueIn: 3, Assignee: "secretary"},
		{Group: "bwera", Title: "Verify the Bwera savings batch", Detail: "Twelve entries are waiting for independent verification.", Priority: "normal", Status: "open", DueIn: 2, Assignee: "verifier"},
		{Group: "nyakato", Title: "Discuss the declined loan with the group", Detail: "Walk the executive through the decline reasons and the reapplication path.", Priority: "high", Status: "open", DueIn: 5, Assignee: "field_officer"},
		{Group: "kigarama", Title: "Confirm Kigarama activation documents", Detail: "Constitution, executive list and bank letter are on file; complete the review.", Priority: "normal", Status: "open", DueIn: 7, Assignee: "credit_approver"},
		{Group: "bunyonyi", Title: "Visit Bunyonyi for cycle close", Detail: "Agree the share-out date and reconcile the cash book before the visit.", Priority: "low", Status: "open", DueIn: 12, Assignee: "field_officer"},
		{Group: "", Title: "Share the monthly portfolio report", Detail: "Sent to management on the last working day.", Priority: "normal", Status: "done", DueIn: -4, Assignee: "management"},
	}
	for _, t := range tasks {
		var gid any
		if t.Group != "" {
			gid = groupIDs[t.Group]
		}
		assignee := userIDs[t.Assignee]
		status := t.Status
		var closedAt any
		if status == "done" {
			closedAt = now.AddDate(0, 0, t.DueIn)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO pamoja.task (group_id, title, detail, assignee_user_id, due_on, status, priority, created_by, closed_at)
			VALUES ($1::uuid, $2, $3, $4::uuid, current_date + $5::int, $6, $7, $8::uuid, $9)`,
			gid, t.Title, t.Detail, assignee, t.DueIn, status, t.Priority, approver, closedAt); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	log.Printf("seed: created %d branches, %d groups, %d staff users", len(branches), len(groups), len(users))
	log.Printf("seed: demo sign-in — phone +256700000001 … +256700000009, password %s", demoPassword)
	return nil
}

func maskPhone(raw string) string {
	normalized := auth.NormalizePhone(raw)
	if len(normalized) < 4 {
		return ""
	}
	return "+256 ••• " + normalized[len(normalized)-3:]
}

func closedAt(status string, heldOn time.Time) any {
	if status == "closed" {
		return heldOn
	}
	return nil
}
