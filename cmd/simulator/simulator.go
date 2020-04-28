// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Binary simulator creates and runs an open-source version of Simulated Hospital.
package main

import (
	"context"
	"flag"
	"math/rand"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/Sirupsen/logrus"
	"github.com/google/simhospital/pkg/config"
	"github.com/google/simhospital/pkg/hl7"
	"github.com/google/simhospital/pkg/hospital"
	"github.com/google/simhospital/pkg/hospital/runner"
	"github.com/google/simhospital/pkg/logging"
	"github.com/google/simhospital/pkg/starter"
)

var (
	log = logging.ForCallerPackage()

	// Flags that control the data that is generated.
	localPath              = flag.String("local_path", "", "Absolute path to the directory where Simulated Hospital is located. Set when running locally to use as a prefix to all default paths")
	locationsFile          = flag.String("locations_file", "configs/hl7_messages/locations.yml", "Path to a YAML file with the definition of locations")
	hardcodedMessagesDir   = flag.String("hardcoded_messages_dir", "configs/hardcoded_messages", "Path to a directory with YAML files that contain hardcoded messages")
	hl7ConfigFile          = flag.String("hl7_config_file", "configs/hl7_messages/hl7.yml", "Path to a YAML file with the possible values of HL7 fields related to how the HL7 standard is used")
	headerConfigFile       = flag.String("header_config_file", "configs/hl7_messages/header.yml", "Path to a YAML file with the configuration for the header of HL7 messages")
	nounsFile              = flag.String("nouns_file", "configs/hl7_messages/third_party/nouns.txt", "Path to a text file containing english nouns")
	surnamesFile           = flag.String("surnames_file", "configs/hl7_messages/third_party/surnames.txt", "Path to a text file containing surnames")
	girlsHistoricNamesFile = flag.String("girls_names", "configs/hl7_messages/third_party/historicname_tcm77-254032-girls.csv", "Path to a CSV file containing historical girls names")
	boysHistoricNamesFile  = flag.String("boys_names", "configs/hl7_messages/third_party/historicname_tcm77-254032-boys.csv", "Path to a CSV file containing historical boys names")
	dataConfigFile         = flag.String("data_config_file", "configs/hl7_messages/data.yml", "Path to a YAML file with the configuration for data to populate HL7 fields that are not relevant to the use of the HL7 standard")
	sampleNotesDir         = flag.String("sample_notes_directory", "configs/hl7_messages/third_party/notes", "Path to a directory with the sample notes")
	clinicalNoteTypesFile  = flag.String("clinical_note_types_file", "configs/hl7_messages/third_party/note_types.txt", "Path to a text file with the Clinical Note types")
	diagnosesFile          = flag.String("diagnoses_file", "configs/hl7_messages/diagnoses.csv", "Path to a CSV file with the diagnoses and how often they occur")
	proceduresFile         = flag.String("procedures_file", "configs/hl7_messages/procedures.csv", "Path to a CSV file with the procedures and how often they occur")
	allergiesFile          = flag.String("allergies_file", "configs/hl7_messages/allergies.csv", "Path to a CSV file with the allergies and how often they occur")
	ethnicityFile          = flag.String("ethnicity_file", "configs/hl7_messages/ethnicity.csv", "Path to a CSV file with the ethnicities and how often they occur")
	patientClassFile       = flag.String("patient_class_file", "configs/hl7_messages/patient_class.csv", "Path to a CSV file with the patient classes and types and how often they occur")
	doctorsFile            = flag.String("doctors_file", "configs/hl7_messages/doctors.yml", "Path to a YAML file with the doctors")
	orderProfilesFile      = flag.String("order_profile_file", "configs/hl7_messages/order_profiles.yml", "Path to a YAML file with the definition of the order profiles")

	// Flags that control the behaviour of Simulated Hospital.
	sleepFor                 = flag.Duration("sleep_for", time.Second, "How long Simulated Hospital sleeps before checking if any new messages need to be generated")
	deletePatientsFromMemory = flag.Bool("delete_patients_from_memory", false, "Whether Simulated Hospital deletes patients after their pathways finish. "+
		"Deleting saves memory but means you can't reuse the patient in another pathway")

	// Flags that control logging and monitoring.
	logLevel             = flag.String("log_level", "INFO", "The logging granularity. One of PANIC, FATAL, ERROR, WARN, INFO, DEBUG. Not case sensitive")
	metricsListenAddress = flag.String("metrics_listen_address", ":9095", "Address on which to expose an HTTP server with a /metrics endpoint for Prometheus to scrape")

	// Flags for sending HL7 messages.
	hl7Timezone           = flag.String("hl7_timezone", "UTC", "The location for the timezone for dates in the generated HL7 messages. The specified location must be installed on the operating system")
	output                = flag.String("output", "stdout", "Where the generated HL7 messages will be sent: [stdout, mllp, file]")
	mllpDestination       = flag.String("mllp_destination", "", "Host:Port to which MLLP messages will be sent; only relevant if -output=mllp")
	mllpKeepAlive         = flag.Bool("mllp_keep_alive", false, "Whether to send keep-alive messages on the MLLP connection; only relevant if -output=mllp")
	mllpKeepAliveInterval = flag.Duration("mllp_keep_alive_interval", time.Minute, "Interval between keep-alive messages; only relevant if -output=mllp and -mllp_keep_alive=true")
	outputFile            = flag.String("output_file", "messages.out", "File path to write messages if -output=file")

	// Flags that control how pathways run.
	pathwaysDir         = flag.String("pathways_dir", "configs/pathways", "Path to a directory with YAML files with definitions of pathways")
	pathwayNames        = flag.String("pathway_names", "", "Comma-separated list of pathway names, or regular expressions that match pathway names, for pathways to run. If empty, all pathways are included")
	excludePathwayNames = flag.String("exclude_pathway_names", "", "Comma-separated list of pathway names, or regular expressions that match pathway names, for the pathways to exclude from running. "+
		"Pathways that match both -pathway_names and -exclude_pathway_names are excluded")
	pathwaysPerHour = flag.Float64("pathways_per_hour", 1, "Number of pathways that should start per hour")

	// Flags that control the dashboard.
	dashboardURI     = flag.String("dashboard_uri", "simulated-hospital", "Base URI at which the dashboard and endpoints are available")
	dashboardAddress = flag.String("dashboard_address", ":8000", "Address for the dashboard to control Simulated Hospital")
	staticDir        = flag.String("static_dir", "web/static", "Directory for static assets")

	// flagset tracks what flags have been set in the command line.
	flagset = make(map[string]bool)
)

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onShutdown(cancel)

	if err := logging.SetLogLevelFromString(*logLevel); err != nil {
		logrus.WithError(err).
			WithField("log_level", *logLevel).
			Fatal("Cannot configure Simulated Hospital logger")
	}
	if err := hl7.TimezoneAndLocation(*hl7Timezone); err != nil {
		logrus.WithError(err).
			WithField("hl7_timezone", *hl7Timezone).
			Fatal("Cannot configure HL7 timezone and location")
	}

	rand.Seed(time.Now().Unix())

	log.Info("Starting Simulated Hospital")
	hr, err := createRunner()
	if err != nil {
		log.WithError(err).Fatal("Cannot create Hospital Runner")
	}
	defer func() {
		if err := hr.Close(); err != nil {
			log.WithError(err).Error("Error when closing Hospital Runner")
		}
	}()
	hr.Run(ctx)
}

// onShutdown handles interrupt signals: SIGINT and SIGTERM,
// and performs a graceful shutdown by calling a cancel function.
func onShutdown(cancel context.CancelFunc) {
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGINT, syscall.SIGTERM)
		<-s
		log.Info("Shutting down gracefully")
		cancel()
	}()
}

func createRunner() (*runner.Hospital, error) {
	flag.Visit(func(f *flag.Flag) { flagset[f.Name] = true })

	var include []string
	if *pathwayNames != "" {
		include = strings.Split(*pathwayNames, ",")
	}
	exclude := strings.Split(*excludePathwayNames, ",")
	paths := hospital.Paths{
		LocationsFile:            addLocalPathIfNotSetAndNotNil(locationsFile, "locations_file"),
		HardcodedMessagesDir:     addLocalPathIfNotSetAndNotNil(hardcodedMessagesDir, "hardcoded_messages_dir"),
		Hl7ConfigFile:            addLocalPathIfNotSetAndNotNil(hl7ConfigFile, "hl7_config_file"),
		HeaderConfigFile:         addLocalPathIfNotSetAndNotNil(headerConfigFile, "header_config_file"),
		DoctorsFile:              addLocalPathIfNotSetAndNotNil(doctorsFile, "doctors_file"),
		OrderProfilesFile:        addLocalPathIfNotSetAndNotNil(orderProfilesFile, "order_profile_file"),
		DeletePatientsFromMemory: *deletePatientsFromMemory,
		PathwayPaths: &hospital.PathwayPaths{
			Dir:          addLocalPathIfNotSet(*pathwaysDir, "pathways_dir"),
			Names:        include,
			ExcludeNames: exclude,
		},
		SenderPaths: &hospital.SenderPaths{
			Output:                *output,
			OutputFile:            *outputFile,
			MllpDestination:       *mllpDestination,
			MllpKeepAlive:         *mllpKeepAlive,
			MllpKeepAliveInterval: mllpKeepAliveInterval,
		},
		DataFiles: &config.DataFiles{
			Nouns:             addLocalPathIfNotSet(*nounsFile, "nouns_file"),
			DataConfig:        addLocalPathIfNotSet(*dataConfigFile, "data_config_file"),
			Procedures:        addLocalPathIfNotSet(*proceduresFile, "procedures_file"),
			Diagnoses:         addLocalPathIfNotSet(*diagnosesFile, "diagnoses_file"),
			Allergies:         addLocalPathIfNotSet(*allergiesFile, "allergies_file"),
			Boys:              addLocalPathIfNotSet(*boysHistoricNamesFile, "boys_names"),
			Girls:             addLocalPathIfNotSet(*girlsHistoricNamesFile, "girls_names"),
			Surnames:          addLocalPathIfNotSet(*surnamesFile, "surnames_file"),
			Ethnicities:       addLocalPathIfNotSet(*ethnicityFile, "ethnicity_file"),
			PatientClass:      addLocalPathIfNotSet(*patientClassFile, "patient_class_file"),
			SampleNotesDir:    addLocalPathIfNotSet(*sampleNotesDir, "sample_notes_directory"),
			ClinicalNoteTypes: addLocalPathIfNotSet(*clinicalNoteTypesFile, "clinical_note_types_file"),
		},
	}

	config, err := hospital.DefaultConfig(paths)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create default hospital config")
	}
	h, err := hospital.NewHospital(config)
	if err != nil {
		return nil, errors.Wrap(err, "cannot instantiate Hospital")
	}
	return runner.New(h, runner.Config{
		PathwayStarter:     &starter.PathwayStarter{Hospital: h, Parser: config.PathwayParser, PathwayManager: config.PathwayManager, Sender: config.Sender},
		PathwaysPerHour:    *pathwaysPerHour,
		DashboardURI:       *dashboardURI,
		DashboardAddress:   *dashboardAddress,
		DashboardStaticDir: addLocalPathIfNotSet(*staticDir, "static_dir"),
		MetricsAddress:     *metricsListenAddress,
		SleepFor:           *sleepFor,
		Clock:              config.Clock,
	})
}

func addLocalPathIfNotSetAndNotNil(f *string, n string) *string {
	if f == nil {
		return nil
	}
	s := addLocalPathIfNotSet(*f, n)
	return &s
}

func addLocalPathIfNotSet(f string, n string) string {
	if flagset[n] {
		return f
	}
	return path.Join(*localPath, f)
}