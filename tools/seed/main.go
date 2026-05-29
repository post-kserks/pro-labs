// Command seed populates a VaultDB instance with realistic MedVault demo data.
//
// It connects over the VaultDB TCP protocol (no auth required there) and is
// idempotent: if the database already contains patients it exits without
// re-seeding, so it is safe to run on every `docker compose up`.
package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
)

const database = "medvault"

func main() {
	rand.Seed(time.Now().UnixNano())

	host := env("VAULTDB_HOST", "vaultdb")
	port := env("VAULTDB_PORT", "5432")
	addr := host + ":" + port
	numPatients := envInt("SEED_PATIENTS", 50)
	numDoctors := envInt("SEED_DOCTORS", 10)

	log.Println("MedVault Seed — connecting to", addr)
	db, err := dial(addr, 120*time.Second)
	if err != nil {
		log.Fatalf("connect failed: %v", err)
	}
	defer db.close()

	// Ensure the database exists and is selected.
	if err := db.execIgnore("CREATE DATABASE "+database+";", "already exists"); err != nil {
		log.Fatalf("create database: %v", err)
	}
	if _, err := db.exec("USE " + database + ";"); err != nil {
		log.Fatalf("use database: %v", err)
	}

	// Idempotency guard.
	if res, err := db.exec("SELECT COUNT(*) FROM patients;"); err == nil {
		if len(res.Rows) > 0 && len(res.Rows[0]) > 0 && atoi(res.Rows[0][0]) > 0 {
			log.Println("Database already seeded — skipping.")
			return
		}
	}

	log.Println("Creating schema...")
	createTables(db)
	createIndexes(db)

	log.Println("Seeding data...")
	seedICDCodes(db)
	doctorIDs := seedDoctors(db, numDoctors)
	patientIDs := seedPatients(db, numPatients)
	visitIDs := seedVisits(db, patientIDs, doctorIDs)
	seedDiagnoses(db, visitIDs, patientIDs, doctorIDs)
	seedPrescriptions(db, visitIDs, patientIDs, doctorIDs)
	seedLabResults(db, patientIDs, visitIDs)
	seedAllergies(db, patientIDs)

	// Real Time-Travel demo data for patient #1.
	seedTimeTravelDemo(db, patientIDs, doctorIDs)

	log.Println("✓ Seed complete!")
	log.Printf("  Patients: %d  Doctors: %d  Visits: %d", len(patientIDs), len(doctorIDs), len(visitIDs))
}

// --- demo data dictionaries ---

var firstNames = []string{
	"Александр", "Дмитрий", "Иван", "Сергей", "Андрей",
	"Анна", "Мария", "Елена", "Ольга", "Наталья",
	"Михаил", "Алексей", "Николай", "Пётр", "Владимир",
	"Татьяна", "Светлана", "Ирина", "Людмила", "Юлия",
}

var lastNames = []string{
	"Иванов", "Смирнов", "Кузнецов", "Попов", "Васильев",
	"Петров", "Соколов", "Михайлов", "Новиков", "Фёдоров",
	"Морозов", "Волков", "Алексеев", "Лебедев", "Семёнов",
}

var specializations = []string{
	"Терапевт", "Кардиолог", "Невролог", "Хирург",
	"Офтальмолог", "Эндокринолог", "Дерматолог", "Педиатр",
}

type icdCode struct{ Code, Description, Category string }

var icdCodes = []icdCode{
	{"J00", "Острый назофарингит (ОРВИ)", "Болезни органов дыхания"},
	{"J06.9", "Острая инфекция верхних дыхательных путей", "Болезни органов дыхания"},
	{"I10", "Эссенциальная гипертензия", "Болезни системы кровообращения"},
	{"E11.9", "Сахарный диабет 2 типа без осложнений", "Болезни эндокринной системы"},
	{"M54.5", "Боль внизу спины", "Болезни костно-мышечной системы"},
	{"K29.7", "Гастрит неуточнённый", "Болезни органов пищеварения"},
	{"J45.9", "Астма неуточнённая", "Болезни органов дыхания"},
	{"F41.1", "Генерализованное тревожное расстройство", "Психические расстройства"},
	{"G43.9", "Мигрень неуточнённая", "Болезни нервной системы"},
	{"I25.1", "Атеросклеротическая болезнь сердца", "Болезни системы кровообращения"},
	{"N39.0", "Инфекция мочевыводящих путей", "Болезни мочеполовой системы"},
	{"L30.9", "Дерматит неуточнённый", "Болезни кожи"},
	{"H52.1", "Миопия", "Болезни глаза"},
	{"Z00.0", "Общий медицинский осмотр", "Факторы, влияющие на здоровье"},
}

var drugNames = []string{
	"Амоксициллин", "Лизиноприл", "Метформин", "Омепразол",
	"Аторвастатин", "Ибупрофен", "Парацетамол", "Амлодипин",
	"Метопролол", "Сальбутамол", "Флуоксетин", "Цетиризин",
}

var bloodTypes = []string{"A+", "A-", "B+", "B-", "AB+", "AB-", "O+", "O-"}

var allergens = []string{
	"Пенициллин", "Аспирин", "Ибупрофен", "Сульфаниламиды",
	"Латекс", "Пыльца", "Кошачья шерсть", "Арахис",
}

// --- schema ---

func createTables(db *vdbConn) {
	tables := []string{
		`CREATE TABLE patients (id INT, first_name VARCHAR(64), last_name VARCHAR(64), birth_date VARCHAR(10), gender VARCHAR(10), phone VARCHAR(20), email VARCHAR(100), blood_type VARCHAR(5), created_at VARCHAR(32), is_active BOOL);`,
		`CREATE TABLE doctors (id INT, first_name VARCHAR(64), last_name VARCHAR(64), specialization VARCHAR(100), license_number VARCHAR(30), phone VARCHAR(20), email VARCHAR(100), is_active BOOL);`,
		`CREATE TABLE visits (id INT, patient_id INT, doctor_id INT, visit_date VARCHAR(32), status VARCHAR(20), chief_complaint TEXT, notes TEXT, created_at VARCHAR(32));`,
		`CREATE TABLE diagnoses (id INT, visit_id INT, patient_id INT, doctor_id INT, icd_code VARCHAR(10), description TEXT, severity VARCHAR(20), diagnosed_at VARCHAR(32), is_active BOOL);`,
		`CREATE TABLE prescriptions (id INT, visit_id INT, patient_id INT, doctor_id INT, drug_name VARCHAR(100), dosage VARCHAR(50), frequency VARCHAR(50), duration VARCHAR(50), instructions TEXT, prescribed_at VARCHAR(32), is_active BOOL);`,
		`CREATE TABLE lab_results (id INT, patient_id INT, visit_id INT, test_name VARCHAR(100), result_value VARCHAR(100), unit VARCHAR(20), reference_min VARCHAR(20), reference_max VARCHAR(20), is_normal BOOL, tested_at VARCHAR(32));`,
		`CREATE TABLE allergies (id INT, patient_id INT, allergen VARCHAR(100), reaction TEXT, severity VARCHAR(20), discovered_at VARCHAR(32), is_active BOOL);`,
		`CREATE TABLE icd_codes (id INT, code VARCHAR(10), description VARCHAR(200), category VARCHAR(100));`,
		`CREATE TABLE timeline_markers (id INT, patient_id INT, label VARCHAR(100), marker_at VARCHAR(40), note VARCHAR(200));`,
	}
	for _, sql := range tables {
		if err := db.execIgnore(sql, "already exists"); err != nil {
			log.Fatalf("create table: %v", err)
		}
	}
	log.Println("  Tables created.")
}

func createIndexes(db *vdbConn) {
	indexes := []string{
		"CREATE INDEX idx_patients_id ON patients (id);",
		"CREATE INDEX idx_visits_patient ON visits (patient_id);",
		"CREATE INDEX idx_diagnoses_patient ON diagnoses (patient_id);",
		"CREATE INDEX idx_prescriptions_patient ON prescriptions (patient_id);",
		"CREATE INDEX idx_lab_results_patient ON lab_results (patient_id);",
		"CREATE INDEX idx_allergies_patient ON allergies (patient_id);",
	}
	for _, sql := range indexes {
		if err := db.execIgnore(sql, "already exists"); err != nil {
			log.Printf("  index warning: %v", err)
		}
	}
	log.Println("  Indexes created.")
}

// --- seeders ---

func seedICDCodes(db *vdbConn) {
	for i, code := range icdCodes {
		mustExec(db, fmt.Sprintf("INSERT INTO icd_codes VALUES (%d, %s, %s, %s);",
			i+1, q(code.Code), q(code.Description), q(code.Category)))
	}
	log.Printf("  ICD codes: %d", len(icdCodes))
}

func seedDoctors(db *vdbConn, n int) []int {
	ids := []int{}
	for i := 0; i < n; i++ {
		id := i + 1
		fn := pick(firstNames)
		ln := pick(lastNames)
		spec := specializations[i%len(specializations)]
		mustExec(db, fmt.Sprintf(
			"INSERT INTO doctors VALUES (%d, %s, %s, %s, %s, %s, %s, true);",
			id, q(fn), q(ln), q(spec), q(fmt.Sprintf("LIC-%05d", id)),
			q(fmt.Sprintf("+7 495 %03d-%02d-%02d", rand.Intn(900)+100, rand.Intn(90)+10, rand.Intn(90)+10)),
			q(fmt.Sprintf("doctor%d@clinic.ru", id))))
		ids = append(ids, id)
	}
	log.Printf("  Doctors: %d", len(ids))
	return ids
}

func seedPatients(db *vdbConn, n int) []int {
	ids := []int{}
	for i := 0; i < n; i++ {
		id := i + 1
		fn := pick(firstNames)
		ln := pick(lastNames)
		age := rand.Intn(62) + 18
		birthYear := time.Now().Year() - age
		birthDate := fmt.Sprintf("%04d-%02d-%02d", birthYear, rand.Intn(12)+1, rand.Intn(28)+1)
		gender := "male"
		if rand.Intn(2) == 0 {
			gender = "female"
		}
		createdAt := time.Now().AddDate(-rand.Intn(3), -rand.Intn(12), 0).UTC().Format(time.RFC3339)
		mustExec(db, fmt.Sprintf(
			"INSERT INTO patients VALUES (%d, %s, %s, %s, %s, %s, %s, %s, %s, true);",
			id, q(fn), q(ln), q(birthDate), q(gender),
			q(fmt.Sprintf("+7 9%02d %03d-%02d-%02d", rand.Intn(90)+10, rand.Intn(900)+100, rand.Intn(90)+10, rand.Intn(90)+10)),
			q(fmt.Sprintf("patient%d@mail.ru", id)), q(pick(bloodTypes)), q(createdAt)))
		ids = append(ids, id)
	}
	log.Printf("  Patients: %d", len(ids))
	return ids
}

func seedVisits(db *vdbConn, patientIDs, doctorIDs []int) []int {
	ids := []int{}
	id := 1
	statuses := []string{"completed", "completed", "completed", "scheduled"}
	complaints := []string{
		"Боль в горле, температура 38.2", "Головная боль, головокружение",
		"Плановый осмотр", "Боль в спине", "Повышенное давление",
		"Кашель, насморк", "Боль в животе", "Контрольный визит",
	}
	for _, patID := range patientIDs {
		visitCount := rand.Intn(7) + 2
		for v := 0; v < visitCount; v++ {
			docID := pick(doctorIDs)
			visitDate := time.Now().AddDate(0, 0, -rand.Intn(365)).UTC().Format(time.RFC3339)
			mustExec(db, fmt.Sprintf(
				"INSERT INTO visits VALUES (%d, %d, %d, %s, %s, %s, '', %s);",
				id, patID, docID, q(visitDate), q(pick(statuses)), q(pick(complaints)), q(visitDate)))
			ids = append(ids, id)
			id++
		}
	}
	log.Printf("  Visits: %d", len(ids))
	return ids
}

func seedDiagnoses(db *vdbConn, visitIDs, patientIDs, doctorIDs []int) {
	id := 1
	severities := []string{"mild", "moderate", "severe"}
	for i, visitID := range visitIDs {
		patID := patientIDs[i%len(patientIDs)]
		code := icdCodes[rand.Intn(len(icdCodes))]
		diagAt := time.Now().AddDate(0, 0, -rand.Intn(365)).UTC().Format(time.RFC3339)
		mustExec(db, fmt.Sprintf(
			"INSERT INTO diagnoses VALUES (%d, %d, %d, %d, %s, %s, %s, %s, true);",
			id, visitID, patID, pick(doctorIDs), q(code.Code), q(code.Description), q(pick(severities)), q(diagAt)))
		id++
	}
	log.Printf("  Diagnoses: %d", id-1)
}

func seedPrescriptions(db *vdbConn, visitIDs, patientIDs, doctorIDs []int) {
	id := 1
	frequencies := []string{"1 раз в день", "2 раза в день", "3 раза в день", "при необходимости"}
	durations := []string{"3 дня", "5 дней", "7 дней", "14 дней", "1 месяц"}
	dosages := []string{"250мг", "500мг", "1000мг", "5мг", "10мг", "20мг"}
	for i, visitID := range visitIDs {
		if rand.Intn(3) == 0 {
			continue
		}
		patID := patientIDs[i%len(patientIDs)]
		prescAt := time.Now().AddDate(0, 0, -rand.Intn(365)).UTC().Format(time.RFC3339)
		mustExec(db, fmt.Sprintf(
			"INSERT INTO prescriptions VALUES (%d, %d, %d, %d, %s, %s, %s, %s, %s, %s, true);",
			id, visitID, patID, pick(doctorIDs), q(pick(drugNames)), q(pick(dosages)),
			q(pick(frequencies)), q(pick(durations)), q("Принимать после еды"), q(prescAt)))
		id++
	}
	log.Printf("  Prescriptions: %d", id-1)
}

func seedLabResults(db *vdbConn, patientIDs, visitIDs []int) {
	id := 1
	tests := []struct{ name, unit, min, max string }{
		{"Гемоглобин", "g/L", "120", "160"},
		{"Глюкоза крови", "mmol/L", "3.9", "6.1"},
		{"Холестерин общий", "mmol/L", "3.0", "5.2"},
		{"Лейкоциты", "10^9/L", "4.0", "9.0"},
		{"АД систолическое", "mmHg", "90", "140"},
	}
	for _, patID := range patientIDs {
		if rand.Intn(3) == 0 {
			continue
		}
		test := tests[rand.Intn(len(tests))]
		val := 3.0 + rand.Float64()*5
		isNorm := rand.Intn(4) != 0
		testedAt := time.Now().AddDate(0, 0, -rand.Intn(180)).UTC().Format(time.RFC3339)
		mustExec(db, fmt.Sprintf(
			"INSERT INTO lab_results VALUES (%d, %d, %d, %s, %s, %s, %s, %s, %t, %s);",
			id, patID, pick(visitIDs), q(test.name), q(fmt.Sprintf("%.1f", val)),
			q(test.unit), q(test.min), q(test.max), isNorm, q(testedAt)))
		id++
	}
	log.Printf("  Lab results: %d", id-1)
}

func seedAllergies(db *vdbConn, patientIDs []int) {
	id := 1
	severities := []string{"mild", "moderate", "severe", "life_threatening"}
	reactions := []string{"Крапивница, зуд", "Отёк Квинке", "Анафилактический шок", "Сыпь, покраснение"}
	for _, patID := range patientIDs {
		if rand.Intn(10) > 2 {
			continue
		}
		discoveredAt := time.Now().AddDate(-rand.Intn(5), -rand.Intn(12), 0).UTC().Format(time.RFC3339)
		mustExec(db, fmt.Sprintf(
			"INSERT INTO allergies VALUES (%d, %d, %s, %s, %s, %s, true);",
			id, patID, q(pick(allergens)), q(pick(reactions)), q(pick(severities)), q(discoveredAt)))
		id++
	}
	log.Printf("  Allergies: %d", id-1)
}

// seedTimeTravelDemo creates genuinely time-separated versions of patient #1's
// diagnosis so AS OF TIMESTAMP returns different states across the slider.
// Each marker timestamp is captured AFTER the relevant write returns, so
// `AS OF marker` is guaranteed to include that version.
func seedTimeTravelDemo(db *vdbConn, patientIDs, doctorIDs []int) {
	if len(patientIDs) == 0 || len(doctorIDs) == 0 {
		return
	}
	patID := patientIDs[0]
	docID := doctorIDs[0]
	nowISO := func() string { return time.Now().UTC().Format(time.RFC3339) }

	type marker struct{ label, at, note string }
	var markers []marker

	// State 1 — initial diagnosis J00 (mild).
	mustExec(db, fmt.Sprintf("INSERT INTO visits VALUES (9001, %d, %d, %s, 'completed', %s, %s, %s);",
		patID, docID, q(nowISO()), q("Кашель, температура"), q("Первичный осмотр"), q(nowISO())))
	mustExec(db, fmt.Sprintf("INSERT INTO diagnoses VALUES (9001, 9001, %d, %d, 'J00', %s, 'mild', %s, true);",
		patID, docID, q("Острый назофарингит (ОРВИ)"), q(nowISO())))
	mustExec(db, fmt.Sprintf("INSERT INTO prescriptions VALUES (9001, 9001, %d, %d, 'Парацетамол', '500мг', '3 раза в день', '5 дней', %s, %s, true);",
		patID, docID, q("После еды"), q(nowISO())))
	time.Sleep(1300 * time.Millisecond)
	markers = append(markers, marker{"Первичный приём", nowISO(), "Диагноз J00 (ОРВИ), лёгкая форма"})
	time.Sleep(400 * time.Millisecond)

	// State 2 — diagnosis refined to J06.9 (moderate).
	mustExec(db, fmt.Sprintf("INSERT INTO visits VALUES (9002, %d, %d, %s, 'completed', %s, %s, %s);",
		patID, docID, q(nowISO()), q("Рецидив, усиление симптомов"), q("Повторный осмотр"), q(nowISO())))
	mustExec(db, "UPDATE diagnoses SET icd_code = 'J06.9', description = 'Острая инфекция верхних дыхательных путей', severity = 'moderate' WHERE id = 9001;")
	mustExec(db, fmt.Sprintf("INSERT INTO prescriptions VALUES (9002, 9002, %d, %d, 'Амоксициллин', '500мг', '3 раза в день', '7 дней', %s, %s, true);",
		patID, docID, q("После еды"), q(nowISO())))
	time.Sleep(1300 * time.Millisecond)
	markers = append(markers, marker{"Уточнённый диагноз", nowISO(), "Диагноз уточнён до J06.9, умеренная тяжесть"})
	time.Sleep(400 * time.Millisecond)

	// State 3 — control visit, condition improving (severity downgraded).
	mustExec(db, fmt.Sprintf("INSERT INTO visits VALUES (9003, %d, %d, %s, 'completed', %s, %s, %s);",
		patID, docID, q(nowISO()), q("Контрольный визит, улучшение"), q("Положительная динамика"), q(nowISO())))
	mustExec(db, "UPDATE diagnoses SET severity = 'mild' WHERE id = 9001;")
	time.Sleep(1300 * time.Millisecond)
	markers = append(markers, marker{"Контрольный визит", nowISO(), "Положительная динамика, тяжесть снижена"})

	// Persist markers for the gateway timeline endpoint.
	for i, m := range markers {
		mustExec(db, fmt.Sprintf("INSERT INTO timeline_markers VALUES (%d, %d, %s, %s, %s);",
			9000+i, patID, q(m.label), q(m.at), q(m.note)))
	}

	log.Printf("  Time-Travel demo: patient #%d, %d markers (J00 → J06.9/moderate → J06.9/mild)", patID, len(markers))
}

// --- helpers ---

func mustExec(db *vdbConn, sql string) {
	if _, err := db.exec(sql); err != nil {
		log.Fatalf("SQL error: %v\nSQL: %s", err, sql)
	}
}

// q renders a SQL single-quoted string literal with escaping.
func q(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return "'" + s + "'"
}

func pick[T any](xs []T) T { return xs[rand.Intn(len(xs))] }

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
