import sys
import json
import os
from crewai import Agent, Task, Crew, Process
from crewai_tools import SerperDevTool
from langchain_groq import ChatGroq

# Get the research topic from the command-line argument
topic = sys.argv[1]

# Set the environment variables for the libraries to use
os.environ["GROQ_API_KEY"] = os.environ.get("GROQ_API_KEY")
os.environ["SERPER_API_KEY"] = os.environ.get("SERPER_API_KEY")

# Initialize the Groq LLM
llm = ChatGroq(
    model_name="groq/llama3-8b-8192"
)

# Initialize the search tool
search_tool = SerperDevTool()

# Define the Researcher Agent
researcher = Agent(
  role='Senior Research Analyst',
  goal=f'Uncover groundbreaking technologies and trends about {topic}',
  backstory="You're a renowned research analyst.",
  tools=[search_tool],
  llm=llm,
  allow_delegation=False,
  verbose=True
)

# Define the Writer Agent
writer = Agent(
  role='Senior Technology Writer',
  goal=f'Craft a compelling and informative blog post about {topic}',
  backstory="You're a famous technology writer.",
  llm=llm,
  allow_delegation=False,
  verbose=True
)

# Define the Tasks
research_task = Task(
  description=f'Conduct a comprehensive analysis of the latest trends in {topic}. Identify key players, innovations, and market forecasts.',
  expected_output='A detailed report summarizing your findings.',
  agent=researcher
)

write_task = Task(
  description='Using the research report, write an engaging blog post. It should be easy to understand, well-structured, and highlight the most important findings.',
  expected_output=f'A 500-word blog post about {topic}, formatted in markdown.',
  agent=writer
)

# --- THIS IS THE FIX ---
# The verbose attribute for the Crew object must be a boolean (True/False).
crew = Crew(
  agents=[researcher, writer],
  tasks=[research_task, write_task],
  process=Process.sequential,
  verbose=True # Use True for detailed logging, not 2
)
# --- END OF FIX ---

# Execute the crew's work
result = crew.kickoff()

# Print the final result as a JSON object to standard output
print(json.dumps({"result": result}))